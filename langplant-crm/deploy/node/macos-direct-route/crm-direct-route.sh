#!/bin/sh
# Keeps the CRM VPS reachable straight through en0 instead of the mesh VPN (utun).
HOST=185.42.26.59
IF=en0
GW=$(ipconfig getoption "$IF" router 2>/dev/null)
[ -n "$GW" ] || exit 0
CUR_IF=$(route -n get "$HOST" 2>/dev/null | awk '/interface:/{print $2}')
CUR_GW=$(route -n get "$HOST" 2>/dev/null | awk '/gateway:/{print $2}')
if [ "$CUR_IF" != "$IF" ] || [ "$CUR_GW" != "$GW" ]; then
  route -n delete -host "$HOST" >/dev/null 2>&1
  route -n add -host "$HOST" "$GW" >/dev/null && logger "crm-direct-route: $HOST via $GW ($IF)"
fi
