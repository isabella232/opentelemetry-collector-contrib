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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/consumer/pdata"
	"gopkg.in/zorkian/go-datadog-api.v2"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/metrics"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/common/ttlmap"
)

func TestMetricValue(t *testing.T) {
	var (
		name  string   = "name"
		value float64  = math.Pi
		ts    uint64   = uint64(time.Now().UnixNano())
		tags  []string = []string{"tool:opentelemetry", "version:0.1.0"}
	)

	metric := metrics.NewGauge(name, ts, value, tags)
	assert.Equal(t, metrics.Gauge, metric.GetType())
	assert.Equal(t, tags, metric.Tags)
}

func TestGetTags(t *testing.T) {
	labels := pdata.NewStringMap()
	labels.InitFromMap(map[string]string{
		"key1": "val1",
		"key2": "val2",
		"key3": "",
	})

	assert.ElementsMatch(t,
		getTags(labels),
		[...]string{"key1:val1", "key2:val2", "key3:n/a"},
	)
}

func TestIsCumulativeMonotonic(t *testing.T) {
	// Some of these examples are from the hostmetrics receiver
	// and reflect the semantic meaning of the metrics there.
	//
	// If the receiver changes these examples should be added here too

	{ // IntSum: Cumulative but not monotonic
		metric := pdata.NewMetric()
		metric.SetName("system.filesystem.usage")
		metric.SetDescription("Filesystem bytes used.")
		metric.SetUnit("bytes")
		metric.SetDataType(pdata.MetricDataTypeIntSum)
		sum := metric.IntSum()
		sum.SetIsMonotonic(false)
		sum.SetAggregationTemporality(pdata.AggregationTemporalityCumulative)

		assert.False(t, isCumulativeMonotonic(metric))
	}

	{ // IntSum: Cumulative and monotonic
		metric := pdata.NewMetric()
		metric.SetName("system.network.packets")
		metric.SetDescription("The number of packets transferred.")
		metric.SetUnit("1")
		metric.SetDataType(pdata.MetricDataTypeIntSum)
		sum := metric.IntSum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pdata.AggregationTemporalityCumulative)

		assert.True(t, isCumulativeMonotonic(metric))
	}

	{ // DoubleSumL Cumulative and monotonic
		metric := pdata.NewMetric()
		metric.SetName("metric.example")
		metric.SetDataType(pdata.MetricDataTypeDoubleSum)
		sum := metric.DoubleSum()
		sum.SetIsMonotonic(true)
		sum.SetAggregationTemporality(pdata.AggregationTemporalityCumulative)

		assert.True(t, isCumulativeMonotonic(metric))
	}

	{ // Not IntSum
		metric := pdata.NewMetric()
		metric.SetName("system.cpu.load_average.1m")
		metric.SetDescription("Average CPU Load over 1 minute.")
		metric.SetUnit("1")
		metric.SetDataType(pdata.MetricDataTypeDoubleGauge)

		assert.False(t, isCumulativeMonotonic(metric))
	}
}

func TestMetricDimensionsToMapKey(t *testing.T) {
	metricName := "metric.name"
	noTags := metricDimensionsToMapKey(metricName, []string{})
	someTags := metricDimensionsToMapKey(metricName, []string{"key1:val1", "key2:val2"})
	sameTags := metricDimensionsToMapKey(metricName, []string{"key2:val2", "key1:val1"})
	diffTags := metricDimensionsToMapKey(metricName, []string{"key3:val3"})

	assert.NotEqual(t, noTags, someTags)
	assert.NotEqual(t, someTags, diffTags)
	assert.Equal(t, someTags, sameTags)
}

func TestMapIntMetrics(t *testing.T) {
	ts := time.Now().UnixNano()
	slice := pdata.NewIntDataPointSlice()
	slice.Resize(1)
	point := slice.At(0)
	point.SetValue(17)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	assert.ElementsMatch(t,
		mapIntMetrics("int64.test", slice),
		[]datadog.Metric{metrics.NewGauge("int64.test", uint64(ts), 17, []string{})},
	)
}

func TestMapDoubleMetrics(t *testing.T) {
	ts := time.Now().UnixNano()
	slice := pdata.NewDoubleDataPointSlice()
	slice.Resize(1)
	point := slice.At(0)
	point.SetValue(math.Pi)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	assert.ElementsMatch(t,
		mapDoubleMetrics("float64.test", slice),
		[]datadog.Metric{metrics.NewGauge("float64.test", uint64(ts), math.Pi, []string{})},
	)
}

func newTTLMap() *ttlmap.TTLMap {
	// don't start the sweeping goroutine
	// since it is not needed
	return ttlmap.New(1800, 3600)
}

const second int64 = 1e9

func TestMapIntMonotonicMetrics(t *testing.T) {
	// Create list of values
	deltas := []int64{1, 2, 200, 3, 7, 0}
	cumulative := make([]int64, len(deltas))
	copy(cumulative, deltas)
	for i := 1; i < len(cumulative); i++ {
		cumulative[i] += cumulative[i-1]
	}

	//Map to OpenTelemetry format
	ts := time.Now().UnixNano()
	slice := pdata.NewIntDataPointSlice()
	slice.Resize(len(cumulative))
	for i, val := range cumulative {
		point := slice.At(i)
		point.SetValue(val)
		point.SetTimestamp(pdata.TimestampUnixNano(ts + int64(i)*second))
	}

	// Map to Datadog format
	metricName := "metric.example"
	output := make([]datadog.Metric, len(deltas))
	for i, val := range deltas {
		output[i] = metrics.NewRate(metricName, uint64(ts+int64(i)*second), float64(val), []string{})
	}

	prevPts := newTTLMap()
	assert.ElementsMatch(t,
		mapIntMonotonicMetrics(metricName, prevPts, slice),
		[]datadog.Metric{
			metrics.NewRate(metricName, uint64(ts+1), 2, []string{}),
			metrics.NewRate(metricName, uint64(ts+2), 200, []string{}),
			metrics.NewRate(metricName, uint64(ts+3), 3, []string{}),
			metrics.NewRate(metricName, uint64(ts+4), 7, []string{}),
			metrics.NewRate(metricName, uint64(ts+5), 0, []string{}),
		},
	)
}

func TestMapIntMonotonicDifferentDimensions(t *testing.T) {
	metricName := "metric.example"
	ts := time.Now().UnixNano()
	slice := pdata.NewIntDataPointSlice()
	slice.Resize(6)

	// No tags
	point := slice.At(0)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	point = slice.At(1)
	point.SetValue(20)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1*second))

	// One tag: valA
	point = slice.At(2)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))
	point.LabelsMap().Insert("key1", "valA")

	point = slice.At(3)
	point.SetValue(30)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1*second))
	point.LabelsMap().Insert("key1", "valA")

	// same tag: valB
	point = slice.At(4)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))
	point.LabelsMap().Insert("key1", "valB")

	point = slice.At(5)
	point.SetValue(40)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1*second))
	point.LabelsMap().Insert("key1", "valB")

	prevPts := newTTLMap()

	assert.ElementsMatch(t,
		mapIntMonotonicMetrics(metricName, prevPts, slice),
		[]datadog.Metric{
			metrics.NewRate(metricName, uint64(ts+1), 20, []string{}),
			metrics.NewRate(metricName, uint64(ts+1), 30, []string{"key1:valA"}),
			metrics.NewRate(metricName, uint64(ts+1), 40, []string{"key1:valB"}),
		},
	)
}

func TestMapIntMonotonicWithReboot(t *testing.T) {
	metricName := "metric.example"
	ts := time.Now().UnixNano()
	slice := pdata.NewIntDataPointSlice()
	slice.Resize(4)

	point := slice.At(0)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	point = slice.At(1)
	point.SetValue(30)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1*second))

	point = slice.At(2)
	point.SetValue(0) // smaller than before, therefore it indicates a reboot
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 2*second))

	point = slice.At(3)
	point.SetValue(20)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 3*second))

	prevPts := newTTLMap()
	assert.ElementsMatch(t,
		mapIntMonotonicMetrics(metricName, prevPts, slice),
		[]datadog.Metric{
			metrics.NewRate(metricName, uint64(ts+1), 30, []string{}),
			metrics.NewRate(metricName, uint64(ts+3), 20, []string{}),
		},
	)
}

func TestMapDoubleMonotonicMetrics(t *testing.T) {
	deltas := []float64{1, 2, 200, 3, 7, 0}
	cumulative := make([]float64, len(deltas))
	copy(cumulative, deltas)
	for i := 1; i < len(cumulative); i++ {
		cumulative[i] += cumulative[i-1]
	}

	//Map to OpenTelemetry format
	ts := time.Now().UnixNano()
	slice := pdata.NewDoubleDataPointSlice()
	slice.Resize(len(cumulative))
	for i, val := range cumulative {
		point := slice.At(i)
		point.SetValue(val)
		point.SetTimestamp(pdata.TimestampUnixNano(ts + int64(i)*second))
	}

	// Map to Datadog format
	metricName := "metric.example"
	output := make([]datadog.Metric, len(deltas))
	for i, val := range deltas {
		output[i] = metrics.NewRate(metricName, uint64(ts+int64(i)), val, []string{})
	}

	prevPts := newTTLMap()
	assert.ElementsMatch(t,
		mapDoubleMonotonicMetrics(metricName, prevPts, slice),
		output,
	)
}

func TestMapDoubleMonotonicDifferentDimension(t *testing.T) {
	metricName := "metric.example"
	ts := time.Now().UnixNano()
	slice := pdata.NewDoubleDataPointSlice()
	slice.Resize(6)

	// No tags
	point := slice.At(0)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	point = slice.At(1)
	point.SetValue(20)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1))

	// One tag: valA
	point = slice.At(2)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))
	point.LabelsMap().Insert("key1", "valA")

	point = slice.At(3)
	point.SetValue(30)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1))
	point.LabelsMap().Insert("key1", "valA")

	// one tag: valB
	point = slice.At(4)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))
	point.LabelsMap().Insert("key1", "valB")

	point = slice.At(5)
	point.SetValue(40)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1))
	point.LabelsMap().Insert("key1", "valB")

	prevPts := newTTLMap()

	assert.ElementsMatch(t,
		mapDoubleMonotonicMetrics(metricName, prevPts, slice),
		[]datadog.Metric{
			metrics.NewRate(metricName, uint64(ts), 20, []string{}),
			metrics.NewRate(metricName, uint64(ts), 30, []string{"key1:valA"}),
			metrics.NewRate(metricName, uint64(ts), 40, []string{"key1:valB"}),
		},
	)
}

func TestMapDoubleMonotonicWithReboot(t *testing.T) {
	metricName := "metric.example"
	ts := time.Now().UnixNano()
	slice := pdata.NewDoubleDataPointSlice()
	slice.Resize(4)

	point := slice.At(0)
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	point = slice.At(1)
	point.SetValue(30)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 1))

	point = slice.At(2)
	point.SetValue(0) // smaller than before, therefore it indicates a reboot
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 2))

	point = slice.At(3)
	point.SetValue(20)
	point.SetTimestamp(pdata.TimestampUnixNano(ts + 3))

	prevPts := newTTLMap()
	assert.ElementsMatch(t,
		mapDoubleMonotonicMetrics(metricName, prevPts, slice),
		[]datadog.Metric{
			metrics.NewRate(metricName, uint64(ts+1), 30, []string{}),
			metrics.NewRate(metricName, uint64(ts+3), 20, []string{}),
		},
	)
}

func TestMapIntHistogramMetrics(t *testing.T) {
	ts := time.Now().UnixNano()
	slice := pdata.NewIntHistogramDataPointSlice()
	slice.Resize(1)
	point := slice.At(0)
	point.SetCount(20)
	point.SetSum(200)
	point.SetBucketCounts([]uint64{2, 18})
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	noBuckets := []datadog.Metric{
		metrics.NewGauge("intHist.test.count", uint64(ts), 20, []string{}),
		metrics.NewGauge("intHist.test.sum", uint64(ts), 200, []string{}),
	}

	buckets := []datadog.Metric{
		metrics.NewGauge("intHist.test.count_per_bucket", uint64(ts), 2, []string{"bucket_idx:0"}),
		metrics.NewGauge("intHist.test.count_per_bucket", uint64(ts), 18, []string{"bucket_idx:1"}),
	}

	assert.ElementsMatch(t,
		mapIntHistogramMetrics("intHist.test", slice, false), // No buckets
		noBuckets,
	)

	assert.ElementsMatch(t,
		mapIntHistogramMetrics("intHist.test", slice, true), // buckets
		append(noBuckets, buckets...),
	)
}

func TestMapDoubleHistogramMetrics(t *testing.T) {
	ts := time.Now().UnixNano()
	slice := pdata.NewDoubleHistogramDataPointSlice()
	slice.Resize(1)
	point := slice.At(0)
	point.SetCount(20)
	point.SetSum(math.Pi)
	point.SetBucketCounts([]uint64{2, 18})
	point.SetTimestamp(pdata.TimestampUnixNano(ts))

	noBuckets := []datadog.Metric{
		metrics.NewGauge("doubleHist.test.count", uint64(ts), 20, []string{}),
		metrics.NewGauge("doubleHist.test.sum", uint64(ts), math.Pi, []string{}),
	}

	buckets := []datadog.Metric{
		metrics.NewGauge("doubleHist.test.count_per_bucket", uint64(ts), 2, []string{"bucket_idx:0"}),
		metrics.NewGauge("doubleHist.test.count_per_bucket", uint64(ts), 18, []string{"bucket_idx:1"}),
	}

	assert.ElementsMatch(t,
		mapDoubleHistogramMetrics("doubleHist.test", slice, false), // No buckets
		noBuckets,
	)

	assert.ElementsMatch(t,
		mapDoubleHistogramMetrics("doubleHist.test", slice, true), // buckets
		append(noBuckets, buckets...),
	)
}
