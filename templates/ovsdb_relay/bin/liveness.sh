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

set -e

RUNDIR="${OVN_RUNDIR:-/run/ovn}"
PIDFILE="${RUNDIR}/ovnsb_relay_db.pid"
CTL="${RUNDIR}/ovnsb_relay_db.ctl"

if [ -f "${PIDFILE}" ] && kill -0 "$(cat "${PIDFILE}")" 2>/dev/null; then
    :
elif ! pidof ovsdb-server >/dev/null 2>&1; then
    echo "ovsdb-server is not running"
    exit 1
fi

if [ ! -S "${CTL}" ]; then
    echo "relay control socket ${CTL} is not available"
    exit 1
fi

if ! timeout 2 ovs-appctl -t "${CTL}" list-commands >/dev/null 2>&1; then
    echo "relay control socket ${CTL} is not responding"
    exit 1
fi

exit 0
