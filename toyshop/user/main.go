package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	corelog "log"

	"github.com/go-kit/kit/log"
	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	stdopentracing "github.com/opentracing/opentracing-go"
	"github.com/packtpublishing/bootstrap-service-mesh-implementations-with-istio/toyshop/user/api"
	"github.com/packtpublishing/bootstrap-service-mesh-implementations-with-istio/toyshop/user/db"
	"github.com/packtpublishing/bootstrap-service-mesh-implementations-with-istio/toyshop/user/db/mongodb"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
)

var (
	port string
	zip  string
)

const (
	ServiceName = "user"
)

func init() {

	flag.StringVar(&zip, "zipkin", os.Getenv("ZIPKIN"), "Zipkin address")
	flag.StringVar(&port, "port", "8084", "Port on which to run")
	db.Register("mongodb", &mongodb.Mongo{})
}

func main() {

	flag.Parse()
	// Mechanical stuff.
	errc := make(chan error)

	// Log domain.
	var logger log.Logger
	{
		logger = log.NewLogfmtLogger(os.Stderr)
		logger = log.With(logger, "ts", log.DefaultTimestampUTC)
		logger = log.With(logger, "caller", log.DefaultCaller)
	}

	// Find service local IP.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		logger.Log("err", err)
		os.Exit(1)
	}
	//localAddr := conn.LocalAddr().(*net.UDPAddr)
	//host := strings.Split(localAddr.String(), ":")[0]
	defer conn.Close()

	var tracer stdopentracing.Tracer
	{
		if zip == "" {
			tracer = stdopentracing.NoopTracer{}
		}
		stdopentracing.InitGlobalTracer(tracer)
	}
	dbconn := false
	for !dbconn {
		err := db.Init()
		if err != nil {
			if err == db.ErrNoDatabaseSelected {
				corelog.Fatal(err)
			}
			corelog.Print(err)
		} else {
			dbconn = true
		}
	}

	fieldKeys := []string{"method"}
	// Service domain.
	var service api.Service
	{
		service = api.NewFixedService()
		service = api.LoggingMiddleware(logger)(service)
		service = api.NewInstrumentingService(
			kitprometheus.NewCounterFrom(
				stdprometheus.CounterOpts{
					Namespace: "microservices_demo",
					Subsystem: "user",
					Name:      "request_count",
					Help:      "Number of requests received.",
				},
				fieldKeys),
			kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
				Namespace: "microservices_demo",
				Subsystem: "user",
				Name:      "request_latency_microseconds",
				Help:      "Total duration of requests in microseconds.",
			}, fieldKeys),
			service,
		)
	}

	// Endpoint domain.
	endpoints := api.MakeEndpoints(service, tracer)

	// HTTP router
	router := api.MakeHTTPHandler(endpoints, logger, tracer)

	// Create and launch the HTTP server.
	go func() {
		logger.Log("transport", "HTTP", "port", port)
		errc <- http.ListenAndServe(fmt.Sprintf(":%v", port), router)
	}()

	// Capture interrupts.
	go func() {
		c := make(chan os.Signal)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		errc <- fmt.Errorf("%s", <-c)
	}()

	logger.Log("exit", <-errc)
}
