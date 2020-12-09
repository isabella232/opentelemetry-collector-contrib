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
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/collector/consumer/pdata"
	"gopkg.in/zorkian/go-datadog-api.v2"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/config"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/metrics"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/common/ttlmap"
)

// getTags maps a stringMap into a slice of Datadog tags
func getTags(labels pdata.StringMap) []string {
	tags := make([]string, 0, labels.Len())
	labels.ForEach(func(key string, value string) {
		if value == "" {
			// Tags can't end with ":" so we replace empty values with "n/a"
			value = "n/a"
		}
		tags = append(tags, fmt.Sprintf("%s:%s", key, value))
	})
	return tags
}

// isCumulativeMonotonic checks if a metric is a cumulative monotonic metric
func isCumulativeMonotonic(md pdata.Metric) bool {
	switch md.DataType() {
	case pdata.MetricDataTypeIntSum:
		return md.IntSum().AggregationTemporality() == pdata.AggregationTemporalityCumulative &&
			md.IntSum().IsMonotonic()
	case pdata.MetricDataTypeDoubleSum:
		return md.DoubleSum().AggregationTemporality() == pdata.AggregationTemporalityCumulative &&
			md.DoubleSum().IsMonotonic()
	}
	return false
}

// metricDimensionsToMapKey maps name and tags to a string to use as an identifier
// The tags order does not matter
func metricDimensionsToMapKey(name string, tags []string) string {
	const separator string = "}{" // These are invalid in tags
	dimensions := append(tags, name)
	sort.Strings(dimensions)
	return strings.Join(dimensions, separator)
}

// mapIntMetrics maps int datapoints into Datadog metrics
func mapIntMetrics(name string, slice pdata.IntDataPointSlice) []datadog.Metric {
	ms := make([]datadog.Metric, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		p := slice.At(i)
		ms = append(ms, metrics.NewGauge(name, uint64(p.Timestamp()), float64(p.Value()), getTags(p.LabelsMap())))
	}
	return ms
}

// mapDoubleMetrics maps double datapoints into Datadog metrics
func mapDoubleMetrics(name string, slice pdata.DoubleDataPointSlice) []datadog.Metric {
	ms := make([]datadog.Metric, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		p := slice.At(i)
		ms = append(ms,
			metrics.NewGauge(name, uint64(p.Timestamp()), p.Value(), getTags(p.LabelsMap())),
		)
	}
	return ms
}

// intCounter keeps the value of an integer
// monotonic counter at a given point in time
type intCounter struct {
	ts    uint64
	value int64
}

// mapIntMonotonicMetrics maps monotonic datapoints into Datadog metrics
func mapIntMonotonicMetrics(name string, prevPts *ttlmap.TTLMap, slice pdata.IntDataPointSlice) []datadog.Metric {
	ms := make([]datadog.Metric, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		p := slice.At(i)
		ts := uint64(p.Timestamp())
		tags := getTags(p.LabelsMap())
		key := metricDimensionsToMapKey(name, tags)

		if c := prevPts.Get(key); c != nil {
			cnt := c.(intCounter)

			// We calculate the time-normalized delta
			dx := float64(p.Value() - cnt.value)
			dt := float64(ts-cnt.ts) / 1e9

			if dt <= 0 {
				// We were given a point older than the one in memory so we drop it
				// We keep the existing point in memory since it is the most recent
				continue
			}

			// if dx < 0, we assume there was a reset, thus we save the point
			// but don't export it (it's the first one so we can't do a delta)
			if dx >= 0 {
				ms = append(ms, metrics.NewRate(name, uint64(p.Timestamp()), dx/dt, tags))
			}

		}
		prevPts.Put(key, intCounter{ts, p.Value()})
	}
	return ms
}

// doubleCounter keeps the value of a double
// monotonic counter at a given point in time
type doubleCounter struct {
	ts    uint64
	value float64
}

// mapDoubleMonotonicMetrics maps monotonic datapoints into Datadog metrics
func mapDoubleMonotonicMetrics(name string, prevPts *ttlmap.TTLMap, slice pdata.DoubleDataPointSlice) []datadog.Metric {
	ms := make([]datadog.Metric, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		p := slice.At(i)
		ts := uint64(p.Timestamp())
		tags := getTags(p.LabelsMap())
		key := metricDimensionsToMapKey(name, tags)

		if c := prevPts.Get(key); c != nil {
			cnt := c.(doubleCounter)

			// We calculate the time-normalized delta
			dx := p.Value() - cnt.value
			dt := float64(ts-cnt.ts) / 1e9

			if dt <= 0 {
				// We were given a point older than the one in memory so we drop it
				// We keep the existing point in memory since it is the most recent
				continue
			}

			// if dx < 0, we assume there was a reset, thus we save the point
			// but don't export it (it's the first one so we can't do a delta)
			if dx >= 0 {
				ms = append(ms, metrics.NewRate(name, uint64(p.Timestamp()), dx/dt, tags))
			}

		}

		prevPts.Put(key, doubleCounter{ts, p.Value()})
	}
	return ms
}

// mapIntHistogramMetrics maps histogram metrics slices to Datadog metrics
//
// A Histogram metric has:
// - The count of values in the population
// - The sum of values in the population
// - A number of buckets, each of them having
//    - the bounds that define the bucket
//    - the count of the number of items in that bucket
//    - a sample value from each bucket
//
// We follow a similar approach to our OpenCensus exporter:
// we report sum and count by default; buckets count can also
// be reported (opt-in), but bounds are ignored.
func mapIntHistogramMetrics(name string, slice pdata.IntHistogramDataPointSlice, buckets bool) []datadog.Metric {
	// Allocate assuming none are nil and no buckets
	ms := make([]datadog.Metric, 0, 2*slice.Len())
	for i := 0; i < slice.Len(); i++ {
		p := slice.At(i)
		ts := uint64(p.Timestamp())
		tags := getTags(p.LabelsMap())

		ms = append(ms,
			metrics.NewGauge(fmt.Sprintf("%s.count", name), ts, float64(p.Count()), tags),
			metrics.NewGauge(fmt.Sprintf("%s.sum", name), ts, float64(p.Sum()), tags),
		)

		if buckets {
			// We have a single metric, 'count_per_bucket', which is tagged with the bucket id. See:
			// https://github.com/DataDog/opencensus-go-exporter-datadog/blob/c3b47f1c6dcf1c47b59c32e8dbb7df5f78162daa/stats.go#L99-L104
			fullName := fmt.Sprintf("%s.count_per_bucket", name)
			for idx, count := range p.BucketCounts() {
				bucketTags := append(tags, fmt.Sprintf("bucket_idx:%d", idx))
				ms = append(ms,
					metrics.NewGauge(fullName, ts, float64(count), bucketTags),
				)
			}
		}
	}
	return ms
}

// mapIntHistogramMetrics maps double histogram metrics slices to Datadog metrics
//
// see mapIntHistogramMetrics docs for further details.
func mapDoubleHistogramMetrics(name string, slice pdata.DoubleHistogramDataPointSlice, buckets bool) []datadog.Metric {
	// Allocate assuming none are nil and no buckets
	ms := make([]datadog.Metric, 0, 2*slice.Len())
	for i := 0; i < slice.Len(); i++ {
		p := slice.At(i)
		ts := uint64(p.Timestamp())
		tags := getTags(p.LabelsMap())

		ms = append(ms,
			metrics.NewGauge(fmt.Sprintf("%s.count", name), ts, float64(p.Count()), tags),
			metrics.NewGauge(fmt.Sprintf("%s.sum", name), ts, p.Sum(), tags),
		)

		if buckets {
			// We have a single metric, 'count_per_bucket', which is tagged with the bucket id. See:
			// https://github.com/DataDog/opencensus-go-exporter-datadog/blob/c3b47f1c6dcf1c47b59c32e8dbb7df5f78162daa/stats.go#L99-L104
			fullName := fmt.Sprintf("%s.count_per_bucket", name)
			for idx, count := range p.BucketCounts() {
				bucketTags := append(tags, fmt.Sprintf("bucket_idx:%d", idx))
				ms = append(ms,
					metrics.NewGauge(fullName, ts, float64(count), bucketTags),
				)
			}
		}
	}
	return ms
}

// mapMetrics maps OTLP metrics into the DataDog format
func mapMetrics(cfg config.MetricsConfig, prevPts *ttlmap.TTLMap, md pdata.Metrics) (series []datadog.Metric, droppedTimeSeries int) {
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		ilms := rm.InstrumentationLibraryMetrics()
		for j := 0; j < ilms.Len(); j++ {
			ilm := ilms.At(j)
			metrics := ilm.Metrics()
			for k := 0; k < metrics.Len(); k++ {
				md := metrics.At(k)
				var datapoints []datadog.Metric
				switch md.DataType() {
				case pdata.MetricDataTypeNone:
					continue
				case pdata.MetricDataTypeIntGauge:
					datapoints = mapIntMetrics(md.Name(), md.IntGauge().DataPoints())
				case pdata.MetricDataTypeDoubleGauge:
					datapoints = mapDoubleMetrics(md.Name(), md.DoubleGauge().DataPoints())
				case pdata.MetricDataTypeIntSum:
					if cfg.SendMonotonic && isCumulativeMonotonic(md) {
						datapoints = mapIntMonotonicMetrics(md.Name(), prevPts, md.IntSum().DataPoints())
					} else {
						datapoints = mapIntMetrics(md.Name(), md.IntSum().DataPoints())
					}
				case pdata.MetricDataTypeDoubleSum:
					if cfg.SendMonotonic && isCumulativeMonotonic(md) {
						datapoints = mapDoubleMonotonicMetrics(md.Name(), prevPts, md.DoubleSum().DataPoints())
					} else {
						datapoints = mapDoubleMetrics(md.Name(), md.DoubleSum().DataPoints())
					}
				case pdata.MetricDataTypeIntHistogram:
					datapoints = mapIntHistogramMetrics(md.Name(), md.IntHistogram().DataPoints(), cfg.Buckets)
				case pdata.MetricDataTypeDoubleHistogram:
					datapoints = mapDoubleHistogramMetrics(md.Name(), md.DoubleHistogram().DataPoints(), cfg.Buckets)
				}

				// Try to get host from resource
				if host, ok := metadata.HostnameFromAttributes(rm.Resource().Attributes()); ok {
					for i := range datapoints {
						datapoints[i].SetHost(host)
					}
				}

				series = append(series, datapoints...)
			}
		}
	}
	return
}
