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

package metadata

import (
	"runtime"

	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/config"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/metadata/ec2"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/metadata/system"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/datadogexporter/utils"
)

// GetHost gets a valid hostname.
func GetHost(logger *zap.Logger, cfg *config.Config) *string {

	// Retrieve from cache
	if cacheVal, ok := utils.GetFromCache(utils.CanonicalHostnameKey); ok {
		return cacheVal.(*string)
	}

	if err := validHostname(cfg.Hostname); err == nil {
		utils.AddToCache(utils.CanonicalHostnameKey, &cfg.Hostname)
		return &cfg.Hostname
	} else if cfg.Hostname != "" {
		logger.Error("Hostname set in configuration is invalid", zap.Error(err))
	}

	// Get system hostname
	hostInfo := system.GetHostInfo(logger)
	hostname := hostInfo.FQDN
	if err := validHostname(hostInfo.FQDN); err != nil {
		// TODO Remove this conditional when we have a Windows workaround
		if runtime.GOOS != "windows" {
			logger.Info("FQDN is not valid", zap.Error(err))
		}

		// FQDN was not valid, fall back to OS hostname
		hostname = hostInfo.OS
	}

	// Get EC2 instance id as canonical hostname
	// if we have a default hostname
	if ec2.IsDefaultHostname(hostname) {
		ec2Info := ec2.GetHostInfo(logger)
		if err := validHostname(ec2Info.InstanceID); err == nil {
			hostname = ec2Info.InstanceID
		} else {
			logger.Info("EC2 instance id is not valid", zap.Error(err))
		}
	}

	// If invalid log the error and continue with that hostname
	if err := validHostname(hostname); err != nil {
		logger.Error("Hostname is not valid", zap.Error(err))
	}

	logger.Debug("Canonical hostname automatically set", zap.String("hostname", hostname))
	utils.AddToCache(utils.CanonicalHostnameKey, &hostname)
	return &hostname
}
