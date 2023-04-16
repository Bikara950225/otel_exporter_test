package main

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// newResource returns a resource describing this application.
func newResource() *resource.Resource {
	r, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("fib"),
		),
	)
	return r
}

// newExporter returns a console exporter.
func newExporter(w io.Writer) (trace.SpanExporter, error) {
	return stdouttrace.New(
		stdouttrace.WithWriter(w),
		// Use human-readable output.
		stdouttrace.WithPrettyPrint(),
		// Do not print timestamps for the demo.
		stdouttrace.WithoutTimestamps(),
	)
}

func fibonacci(ctx context.Context, n uint64) uint64 {
	spanName := fmt.Sprintf("fibonacci-%d", n)
	ctx, span := otel.Tracer("fibonacci").Start(ctx, spanName)

	span.SetAttributes(attribute.KeyValue{
		Key: "timestamp", Value: attribute.Int64Value(time.Now().UnixNano()),
	})
	if n <= 1 {
		span.End()
		return n
	}
	span.End()

	// error
	//span.RecordError(err)
	//span.SetStatus(codes.Error, err)

	return fibonacci(ctx, n-1) + fibonacci(ctx, n-2)
}

type nestedSpanHandler struct{}

func (s *nestedSpanHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	ctx, span := otel.Tracer("nested").Start(req.Context(), "parent")
	defer span.End()

	span.AddEvent("parent event")

	span.SetAttributes(attribute.KeyValue{
		Key: "parentId", Value: attribute.Int64Value(time.Now().UnixNano()),
	})

	func(ctx context.Context) {
		ctx, span := otel.Tracer("nested").Start(ctx, "child")
		defer span.End()

		span.SetAttributes(attribute.KeyValue{
			Key: "childId", Value: attribute.Int64Value(time.Now().UnixNano()),
		})
	}(ctx)
}

type fibonacciHandler struct{}

func (s *fibonacciHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	n := req.URL.Query().Get("n")
	nCount, err := strconv.ParseInt(n, 10, 64)
	if err != nil {
		resp.WriteHeader(http.StatusBadRequest)
		resp.Write([]byte("n is not a number"))
		return
	}
	ret := fibonacci(req.Context(), uint64(nCount))
	resp.WriteHeader(http.StatusOK)
	resp.Write([]byte(strconv.FormatUint(ret, 10)))
}

func main() {
	countCollector := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "countPerSec",
	}, []string{
		"id", "database",
	})

	err := prometheus.Register(countCollector)
	if err != nil {
		panic(err)
	}

	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			<-timer.C
			countCollector.WithLabelValues("1", "db1").Inc()
			timer.Reset(time.Second)
		}
	}()

	// otel SDK
	// Write telemetry data to a file.
	f, err := os.Create("traces.txt")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	// 创建一个新的exporter，将telemetry数据写出到文件
	exp, err := newExporter(f)
	if err != nil {
		log.Fatalln(err.Error())
	}
	// 新建一个TracerProvider, 以trace.WithBatcher把exporter注册上去
	tracerProvider := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(newResource()),
	)
	defer func() {
		if err = tracerProvider.Shutdown(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()
	// 把tracerProvider注册到全剧
	otel.SetTracerProvider(tracerProvider)

	http.Handle("/metric", promhttp.Handler())
	http.Handle("/fibonacci", &fibonacciHandler{})
	http.Handle("/nested", &nestedSpanHandler{})
	if err = http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalln(err.Error())
	}
}
