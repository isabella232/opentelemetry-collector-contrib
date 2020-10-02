// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datadogexporter

import (
	"context"
	"fmt"

	"github.com/DataDog/datadog-agent/pkg/trace/obfuscate"
	"github.com/DataDog/datadog-agent/pkg/trace/pb"
	"go.opentelemetry.io/collector/consumer/pdata"
	"go.uber.org/zap"
)

type traceExporter struct {
	logger         *zap.Logger
	cfg            *Config
	edgeConnection TraceEdgeConnection
	obfuscator     *obfuscate.Obfuscator
	tags           []string
}

func newTraceExporter(logger *zap.Logger, cfg *Config) (*traceExporter, error) {
	// removes potentially sensitive info and PII, approach taken from serverless approach
	// https://github.com/DataDog/datadog-serverless-functions/blob/11f170eac105d66be30f18eda09eca791bc0d31b/aws/logs_monitoring/trace_forwarder/cmd/trace/main.go#L43
	obfuscator := obfuscate.NewObfuscator(&obfuscate.Config{
		ES: obfuscate.JSONSettings{
			Enabled: true,
		},
		Mongo: obfuscate.JSONSettings{
			Enabled: true,
		},
		RemoveQueryString: true,
		RemovePathDigits:  true,
		RemoveStackTraces: true,
		Redis:             true,
		Memcached:         true,
	})

	// Calculate tags at startup
	tags := cfg.TagsConfig.GetTags(false)
	// TODO:
	// use passed in config values for site and api key instead of hardcoded
	exporter := &traceExporter{
		logger:         logger,
		cfg:            cfg,
		edgeConnection: CreateTraceEdgeConnection(cfg.Traces.TCPAddr.Endpoint, cfg.API.Key, false),
		obfuscator:     obfuscator,
		tags:           tags,
	}

	return exporter, nil
}

func (exp *traceExporter) pushTraceData(
	ctx context.Context,
	td pdata.Traces,
) (int, error) {

	// convert traces to datadog traces and group trace payloads by env
	// we largely apply the same logic as the serverless implementation, simplified a bit
	// https://github.com/DataDog/datadog-serverless-functions/blob/f5c3aedfec5ba223b11b76a4239fcbf35ec7d045/aws/logs_monitoring/trace_forwarder/cmd/trace/main.go#L61-L83
	ddTraces, err := convertToDatadogTd(td, exp.cfg, exp.tags)

	if err != nil {
		exp.logger.Info(fmt.Sprintf("Failed to convert traces with error %v\n", err))
		return 0, err
	}

	// group the traces by env to reduce the number of flushes
	aggregatedTraces := aggregateTracePayloadsByEnv(ddTraces)

	// security/obfuscation for db, query strings, stack traces, pii, etc
	// TODO: is there any config we want here? OTEL has their own pipeline for regex obfuscation
	ObfuscatePayload(exp.obfuscator, aggregatedTraces)

	for _, ddTracePayload := range aggregatedTraces {
		// currently we don't want to do retries since api endpoints may not dedupe in certain situations
		// adding a helper function here to make custom retry logic easier in the future
		exp.pushWithRetry(ddTracePayload, 1, func() error {
			return nil
		})
	}

	return len(aggregatedTraces), nil
}

// gives us flexibility to add custom retry logic later
func (exp *traceExporter) pushWithRetry(ddTracePayload *pb.TracePayload, maxRetries int, fn func() error) error {
	err := exp.edgeConnection.SendTraces(context.Background(), ddTracePayload, maxRetries)

	if err != nil {
		exp.logger.Info(fmt.Sprintf("Failed to send traces with error %v\n", err))
	}

	// this is for generating metrics like hits, errors, and latency, it uses a separate endpoint than Traces
	stats := ComputeAPMStats(ddTracePayload)
	errStats := exp.edgeConnection.SendStats(context.Background(), stats, maxRetries)

	if errStats != nil {
		exp.logger.Info(fmt.Sprintf("Failed to send trace stats with error %v\n", errStats))
	}

	return fn()
}
