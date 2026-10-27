#!/bin/sh
set -eu

load_secret() {
  variable="$1"
  secret_name="$2"
  secret_file="/run/secrets/$secret_name"
  value=""
  if [ -f "$secret_file" ]; then
    value="$(sed -n '1p' "$secret_file")"
  fi
  export "$variable=$value"
  unset value
}

load_secret UPSTREAM_GAPI_ACCESS_TOKEN upstream_gapi_access_token
load_secret UPSTREAM_GAPI_USER_ID upstream_gapi_user_id
load_secret UPSTREAM_U_GATEWAY_API_KEY upstream_u_gateway_api_key
load_secret UPSTREAM_INROI_API_KEY upstream_inroi_api_key
load_secret UPSTREAM_DORO_ACCESS_TOKEN upstream_doro_access_token
load_secret UPSTREAM_DORO_USER_ID upstream_doro_user_id
load_secret UPSTREAM_TOKEN_API_KEY upstream_token_api_key
load_secret UPSTREAM_OTOK_API_KEY upstream_otok_api_key
load_secret UPSTREAM_MAX_API_KEY upstream_max_api_key
load_secret UPSTREAM_VIAPI_API_KEY upstream_viapi_api_key
load_secret UPSTREAM_OPR_TOKENTS_API_KEY upstream_opr_tokents_api_key
load_secret SMTP_PASS smtp_pass
load_secret ALERT_CRON_TOKEN alert_cron_token
if [ -f /run/secrets/monitor_snapshot_token ]; then
  load_secret MONITOR_SNAPSHOT_TOKEN monitor_snapshot_token
fi

exec node /app/lite/server.mjs
