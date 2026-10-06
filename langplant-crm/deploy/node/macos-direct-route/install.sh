#!/bin/sh
set -e
cd "$(dirname "$0")"
mkdir -p /usr/local/sbin
install -m 755 -o root -g wheel crm-direct-route.sh /usr/local/sbin/crm-direct-route.sh
install -m 644 -o root -g wheel com.langplant.crm-direct-route.plist /Library/LaunchDaemons/
launchctl bootout system/com.langplant.crm-direct-route 2>/dev/null || true
launchctl bootstrap system /Library/LaunchDaemons/com.langplant.crm-direct-route.plist
sleep 2
route -n get 185.42.26.59 | grep -E 'gateway|interface'
