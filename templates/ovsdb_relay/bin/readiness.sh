#!/bin/bash
#
# Copyright 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may
# not use this file except in compliance with the License. You may obtain
# a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
# WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
# License for the specific language governing permissions and limitations
# under the License.

# The relay is ready when its local database is loaded and it has an active
# session to at least one upstream server (_Server.connected=true).

set -e

SOCKET="${OVN_RUNDIR:-/run/ovn}/ovnsb_relay_db.sock"

if [ ! -S "${SOCKET}" ]; then
    echo "relay unix socket ${SOCKET} is not available yet"
    exit 1
fi

if ! OUTPUT=$(ovsdb-client --timeout=5 dump "unix:${SOCKET}" _Server Database connected name 2>&1); then
    echo "failed to query relay database: ${OUTPUT}"
    exit 1
fi

# The _Server database contains a row for OVN_Southbound and one for _Server
# itself. Both rows have a connected column; only the OVN_Southbound row tells
# us whether the relay has an active upstream connection.
if ! echo "${OUTPUT}" | awk '$NF ~ /OVN_Southbound/ && $1 == "true" { found=1 } END { exit !found }'; then
    echo "relay is not connected to an upstream server yet"
    exit 1
fi

exit 0
