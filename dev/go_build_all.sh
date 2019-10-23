#!/bin/bash

# Build clickhouse-operator
# Do not forget to update version

# Source configuration
CUR_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"

"${CUR_DIR}"/go_build_metrics_exporter.sh
"${CUR_DIR}"/go_build_operator.sh
