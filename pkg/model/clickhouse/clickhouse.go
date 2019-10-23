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

package clickhouse

import (
	"context"
	sqlmodule "database/sql"
	"fmt"
	"github.com/golang/glog"
	_ "github.com/mailru/go-clickhouse"
	"strconv"
	"time"
)

const (
	// http://user:password@host:8123/
	chDsnUrlPattern = "http://%s%s:%s/"
	defaultTimeout  = 10 * time.Second
)

type Conn struct {
	hostname string
	username string
	password string
	port     int

	timeout time.Duration
}

func New(hostname, username, password string, port int) *Conn {
	return &Conn{
		hostname: hostname,
		username: username,
		password: password,
		port:     port,
		timeout:  defaultTimeout,
	}
}

// makeUsernamePassword makes "username:password" pair for connection
func (c *Conn) makeUsernamePassword() string {
	if c.username == "" && c.password == "" {
		return ""
	}

	// password may be omitted
	if c.password == "" {
		return c.username + "@"
	}

	// Expecting both username and password to be in place
	return c.username + ":" + c.password + "@"
}

// makeDsn makes ClickHouse DSN
func (c *Conn) makeDsn() string {
	return fmt.Sprintf(chDsnUrlPattern, c.makeUsernamePassword(), c.hostname, strconv.Itoa(c.port))
}

// Query runs given sql query
func (c *Conn) Query(sql string) (*sqlmodule.Rows, error) {
	if len(sql) == 0 {
		return nil, nil
	}

	// Query should be deadlined
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(c.timeout))
	defer cancel()

	dsn := c.makeDsn()
	//glog.V(1).Infof("Query ClickHouse DSN: %s", dsn)
	connect, err := sqlmodule.Open("clickhouse", dsn)
	if err != nil {
		glog.V(1).Infof("FAILED Open(%s) %v for SQL: %s", dsn, err, sql)
		return nil, err
	}

	if err := connect.PingContext(ctx); err != nil {
		glog.V(1).Infof("FAILED Ping(%s) %v for SQL: %s", dsn, err, sql)
		return nil, err
	}

	rows, err := connect.QueryContext(ctx, sql)
	if err != nil {
		glog.V(1).Infof("FAILED Query(%s) %v for SQL: %s", dsn, err, sql)
		return nil, err
	}

	// glog.V(1).Infof("clickhouse.Query(%s):'%s'", c.Hostname, sql)

	return rows, nil
}

// Exec runs given sql query
func (c *Conn) Exec(sql string) error {
	if len(sql) == 0 {
		return nil
	}

	// Query should be deadlined
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(c.timeout))
	defer cancel()

	dsn := c.makeDsn()
	//glog.V(1).Infof("Exec ClickHouse DSN: %s", dsn)
	connect, err := sqlmodule.Open("clickhouse", dsn)
	if err != nil {
		glog.V(1).Infof("FAILED Open(%s) %v for SQL: %s", dsn, err, sql)
		return err
	}

	if err := connect.PingContext(ctx); err != nil {
		glog.V(1).Infof("FAILED Ping(%s) %v for SQL: %s", dsn, err, sql)
		return err
	}

	_, err = connect.ExecContext(ctx, sql)

	if err != nil {
		glog.V(1).Infof("FAILED Exec(%s) %v for SQL: %s", dsn, err, sql)
		return err
	}

	// glog.V(1).Infof("clickhouse.Exec(%s):'%s'", c.Hostname, sql)

	return nil
}
