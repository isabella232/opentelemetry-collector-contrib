module github.com/DataDog/opentelemetry-collector-contrib/exporter/datadogexporter

go 1.15

require (
	github.com/DataDog/datadog-agent/pkg/trace/exportable v0.0.0-20201009092250-ef1209a2f015
	github.com/stretchr/testify v1.6.1
	github.com/zorkian/go-datadog-api v2.29.0+incompatible // indirect
	go.opencensus.io v0.22.4
	go.opentelemetry.io/collector v0.11.1-0.20201006165100-07236c11fb27
	go.uber.org/zap v1.16.0
	gopkg.in/DataDog/dd-trace-go.v1 v1.26.0
	gopkg.in/zorkian/go-datadog-api.v2 v2.29.0
)
