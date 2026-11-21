#!/bin/bash
# Boots the locally built preview image behind a cloudflared tunnel, so the Elastic workflow can
# call back into it, and points the plugin at a local-test copy of the workflow that accepts
# *.trycloudflare.com. Needs a running tunnel to localhost:8076; pass its https URL. Any further
# arguments go to docker run, e.g. -e MM_SERVICESETTINGS_ENABLECOMMANDS=true. PW keeps a password.
set -euo pipefail
URL=${1:?usage: local-preview.sh https://<name>.trycloudflare.com [docker run args]}
shift
IMAGE=${IMAGE:-mattermost-preview:meridian-soc-insider-threat-1.13}
C=preview-meridian-soc-insider-threat
P=com.esethna.insider-threat-demo
cd "$(dirname "$0")/.."

SSL_CERT_FILE=$(python3 -c "import certifi;print(certifi.where())") \
DEMO_CALLBACK_SECRET=$(cat ~/.mattermost/insider-threat-demo-callback-secret) \
PREVIEW_DOMAIN=trycloudflare.com WORKFLOW_ID=insider-threat-preview-start-local \
  python3 elastic/install_workflow.py install

docker rm -f $C >/dev/null 2>&1 || true
docker run -d --name $C -p 8076:8065 -p 8486:8443/udp -p 8486:8443/tcp \
  -e MM_CALLS_ICE_HOST_OVERRIDE=127.0.0.1 -e MM_CALLS_ICE_HOST_PORT_OVERRIDE=8486 \
  -e MM_SERVICESETTINGS_SITEURL="$URL" -e MM_SERVICESETTINGS_ENABLELOCALMODE=true \
  --env-file ~/.mattermost/preview-llm.env --env-file ~/.mattermost/preview-secrets.env "$@" "$IMAGE" >/dev/null
for _ in $(seq 1 120); do curl -sf localhost:8076/api/v4/system/ping >/dev/null && break; sleep 1; done
docker logs $C 2>&1 | grep -E '^boot: (filled|guardrails|WARNING)' || true

m() { docker exec $C mmctl --local "$@"; }
m config set PluginSettings.Plugins.$P.workflowid insider-threat-preview-start-local >/dev/null
m config set PluginSettings.Plugins.$P.previewdomain trycloudflare.com >/dev/null
m plugin disable $P >/dev/null && m plugin enable $P >/dev/null

PW=${PW:-"Local-$(openssl rand -hex 12)"}
m user change-password priya.raman --password "$PW" >/dev/null
echo "open $URL and sign in as priya.raman / $PW"
