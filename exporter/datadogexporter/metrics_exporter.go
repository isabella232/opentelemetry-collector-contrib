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

	"go.opentelemetry.io/collector/consumer/pdata"
	"go.uber.org/zap"
	"gopkg.in/zorkian/go-datadog-api.v2"
)

type metricsExporter struct {
	logger *zap.Logger
	cfg    *Config
	client *datadog.Client
	tags   []string
}

func newMetricsExporter(logger *zap.Logger, cfg *Config) (*metricsExporter, error) {
	client := datadog.NewClient(cfg.API.Key, "")
	client.SetBaseUrl(cfg.Metrics.TCPAddr.Endpoint)

	// Calculate tags at startup
	tags := cfg.TagsConfig.GetTags(false)

	return &metricsExporter{logger, cfg, client, tags}, nil
}

func (exp *metricsExporter) processMetrics(series *Series) {
	addNamespace := exp.cfg.Metrics.Namespace != ""
	overrideHostname := exp.cfg.Hostname != ""
	addTags := len(exp.tags) > 0

	for i := range series.metrics {
		if addNamespace {
			newName := exp.cfg.Metrics.Namespace + *series.metrics[i].Metric
			series.metrics[i].Metric = &newName
		}

		if overrideHostname || series.metrics[i].GetHost() == "" {
			series.metrics[i].Host = GetHost(exp.cfg)
		}

		if addTags {
			series.metrics[i].Tags = append(series.metrics[i].Tags, exp.tags...)
		}

	}
}

func (exp *metricsExporter) PushMetricsData(ctx context.Context, md pdata.Metrics) (int, error) {
	series, droppedTimeSeries := MapMetrics(exp.logger, exp.cfg.Metrics, md)
	exp.processMetrics(&series)

	err := exp.client.PostMetrics(series.metrics)
	return droppedTimeSeries, err
}