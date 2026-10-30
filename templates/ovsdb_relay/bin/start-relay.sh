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

: "${DB_NAME:=OVN_Southbound}"
: "${OVN_RUNDIR:=/run/ovn}"
: "${PORT:=6642}"
: "${USE_REMOTE_IN_DB:=true}"
: "${LOG_LEVEL:=info}"

if [ -z "${UPSTREAM}" ]; then
    echo "UPSTREAM is not set" >&2
    exit 1
fi

TLS_SCHEME="ptcp"
TLS_ARGS=()
if [ "${TLS_ENABLED}" = "true" ]; then
    TLS_SCHEME="pssl"
    TLS_ARGS=(
        "--private-key=${OVNDB_KEY_PATH}"
        "--certificate=${OVNDB_CERT_PATH}"
        "--ca-cert=${OVNDB_CA_CERT_PATH}"
    )
fi

REMOTES=()
if [ "${USE_REMOTE_IN_DB}" = "true" ]; then
    # In relay mode the listening connection is stored in SB_Global.connections.
    # It binds the SB database's own port (6642 for OVN SB), which is not
    # necessarily the port this relay advertises.
    REMOTES+=("--remote=db:${DB_NAME},SB_Global,connections")
fi
# Always listen on the advertised port: dnsmasq/DNSData clients connect to pod
# IPs directly and bypass Service port mapping. When the db: remote resolves to
# the same target, ovsdb-server merges the entries and keeps the Connection
# row's options (role, inactivity probe) on the shared remote.
REMOTES+=("--remote=${TLS_SCHEME}:${PORT}:[::]")
REMOTES+=("--remote=punix:${OVN_RUNDIR}/ovnsb_relay_db.sock")

exec ovsdb-server \
    "${REMOTES[@]}" \
    --pidfile="${OVN_RUNDIR}/ovnsb_relay_db.pid" \
    --unixctl="${OVN_RUNDIR}/ovnsb_relay_db.ctl" \
    "${TLS_ARGS[@]}" \
    --log-file=/dev/stdout -vconsole:"${LOG_LEVEL}" \
    "relay:${DB_NAME}:${UPSTREAM}"
