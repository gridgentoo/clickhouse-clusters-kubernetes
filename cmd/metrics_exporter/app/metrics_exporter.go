// Copyright 2019 Altinity Ltd and/or its affiliates. All rights reserved.
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

package app

import (
	"context"
	"flag"
	"fmt"
	"github.com/altinity/clickhouse-operator/pkg/apis/metrics"
	chopconfig "github.com/altinity/clickhouse-operator/pkg/config"
	"github.com/altinity/clickhouse-operator/pkg/version"
	"github.com/golang/glog"
	"os"
	"os/signal"
	"syscall"
)

// Prometheus exporter defaults
const (
	defaultMetricsEndpoint = ":8888"
	defaultChiListEP       = ":8888"

	metricsPath = "/metrics"
	chiListPath = "/chi"
)

// CLI parameter variables
var (
	// versionRequest defines request for clickhouse-operator version report. Operator should exit after version printed
	versionRequest bool

	// chopConfigFile defines path to clickhouse-operator config file to be used
	chopConfigFile string

	// metricsEP defines metrics end-point IP address
	metricsEP string

	chiListEP string
)

func init() {
	flag.BoolVar(&versionRequest, "version", false, "Display clickhouse-operator version and exit")
	flag.StringVar(&chopConfigFile, "config", "", "Path to clickhouse-operator config file.")
	flag.StringVar(&metricsEP, "metrics-endpoint", defaultMetricsEndpoint, "The Prometheus exporter endpoint.")
	flag.StringVar(&chiListEP, "chi-list-endpoint", defaultChiListEP, "The CHI list endpoint.")
	flag.Parse()
}

// Run is an entry point of the application
func Run() {
	if versionRequest {
		fmt.Printf("%s\n", version.Version)
		os.Exit(0)
	}

	// Set OS signals and termination context
	ctx, cancelFunc := context.WithCancel(context.Background())
	stopChan := make(chan os.Signal, 2)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopChan
		cancelFunc()
		<-stopChan
		os.Exit(1)
	}()

	//
	// Create operator config
	//
	chopConfigManager := chopconfig.NewConfigManager(nil, chopConfigFile)
	if err := chopConfigManager.Init(); err != nil {
		glog.Fatalf("Unable to build config file %v\n", err)
		os.Exit(1)
	}

	glog.V(1).Info("Starting metrics exporter\n")

	metrics.StartMetricsREST(
		chopConfigManager.Config().ChUsername, chopConfigManager.Config().ChPassword, chopConfigManager.Config().ChPort,
		metricsEP, metricsPath,
		chiListEP, chiListPath,
	)

	<-ctx.Done()
}
