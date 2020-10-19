module github.com/DataDog/opentelemetry-collector-contrib/exporter/datadogexporter

go 1.15

require (
	github.com/DataDog/datadog-agent/pkg/trace/exportable v0.0.0-20201016145401-4646cf596b02
	github.com/DataDog/viper v1.8.0 // indirect
	github.com/census-instrumentation/opencensus-proto v0.3.0
	github.com/cihub/seelog v0.0.0-20170130134532-f561c5e57575 // indirect
	github.com/gogo/protobuf v1.3.1
	github.com/klauspost/compress v1.10.10
	github.com/stretchr/testify v1.6.1
	github.com/zorkian/go-datadog-api v2.29.0+incompatible // indirect
	go.opencensus.io v0.22.4
	go.opentelemetry.io/collector v0.11.1-0.20201001213035-035aa5cf6c92
	go.uber.org/zap v1.16.0
	google.golang.org/protobuf v1.25.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.26.0
	gopkg.in/zorkian/go-datadog-api.v2 v2.29.0
)
