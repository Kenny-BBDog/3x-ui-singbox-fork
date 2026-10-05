#!/usr/bin/env bash
#
# Deploy the panel binary to the production hosts.
#
# Implements specs/0001-deploy-pipeline.md. Run this from the operator's machine
# (Git Bash on Windows), not from a host:
#
#   ./deploy/deploy.sh                 # current dev-latest, both hosts
#   ./deploy/deploy.sh --tag v3.8.6    # a specific release tag
#   ./deploy/deploy.sh --hosts dmit    # one host
#   ./deploy/deploy.sh --dry-run       # download, verify, stage; no swap
#   ./deploy/deploy.sh --health-only   # just check the hosts now
#
# Why this is a local script and not a GitHub workflow: it needs the SSH key that
# reaches production. Keeping that key off GitHub keeps the blast radius at the
# operator's machine. See specs/0001-deploy-pipeline.md, Non-goals.
#
# Deploy is disruptive to live connections: x-ui is the parent of xray and
# sing-box, so the restart drops every client for a few seconds. Run it in the
# low-traffic window.

set -Eeuo pipefail

# ------------------------------------------------------------------ settings

REPO="Kenny-BBDog/3x-ui-singbox-fork"
ARTIFACT="x-ui-linux-amd64.tar.gz"
REMOTE_BIN="/usr/local/x-ui/x-ui"
SERVICE="x-ui"

# DMIT is the master panel and the jump host for LA (DMIT holds the key LA
# trusts), matching how this estate is already administered.
DMIT_HOST="179.255.106.182"
LA_HOST="156.225.88.212"
LA_JUMP_KEY="/root/.ssh/sub2api_migration_ed25519"

# Health-gate endpoints, probed from the host itself. LA's panel is plain HTTP on
# 25073 under a random base path, not TLS behind nginx, so it is not shared.
DMIT_BASE="https://vpn.flintic.uk/vpn-admin/"
DMIT_ORIGIN="https://vpn.flintic.uk"
DMIT_CURL_EXTRA="--resolve vpn.flintic.uk:443:127.0.0.1"
LA_BASE="http://127.0.0.1:25073/Cul7KGMTQ8sEpXsfsN/"
LA_ORIGIN="http://127.0.0.1:25073"

TAG="dev-latest"
HOSTS="both"
DRY_RUN=0
HEALTH_ONLY=0
SSH_KEY="${SSH_KEY:-$HOME/.ssh/vps_clean.pem}"

# --------------------------------------------------------------------- output

if [[ -t 1 ]]; then
  C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_RED=$'\033[31m'
  C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_BLUE=$'\033[34m'
else
  C_RESET=""; C_BOLD=""; C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""
fi

log()  { printf '%s\n' "$*"; }
step() { printf '\n%s==> %s%s\n' "$C_BOLD$C_BLUE" "$*" "$C_RESET"; }
ok()   { printf '    %s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn() { printf '    %s!%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
die()  { printf '\n%sERROR:%s %s\n' "$C_RED$C_BOLD" "$C_RESET" "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Deploy the panel binary to the production hosts.

  ./deploy/deploy.sh                 current dev-latest, both hosts
  ./deploy/deploy.sh --tag v3.8.6    a specific release tag
  ./deploy/deploy.sh --hosts dmit    one host
  ./deploy/deploy.sh --dry-run       download, verify, stage; no swap
  ./deploy/deploy.sh --health-only   just check the hosts now

Options:
  --tag <tag>        Release tag to deploy (default: dev-latest)
  --hosts <which>    dmit | la | both (default: both)
  --key <path>       SSH private key for DMIT (default: ~/.ssh/vps_clean.pem)
  --dry-run          Download, verify and stage; do not swap the binary
  --health-only      Run the health gate against the hosts as they are
  -h, --help         This text

The restart drops client connections for a few seconds; deploy in the
low-traffic window. A failed health gate rolls back automatically.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)         TAG="${2:?--tag needs a value}"; shift 2 ;;
    --hosts)       HOSTS="${2:?--hosts needs a value}"; shift 2 ;;
    --key)         SSH_KEY="${2:?--key needs a value}"; shift 2 ;;
    --dry-run)     DRY_RUN=1; shift ;;
    --health-only) HEALTH_ONLY=1; shift ;;
    -h|--help)     usage; exit 0 ;;
    *)             die "unknown argument: $1 (try --help)" ;;
  esac
done

case "$HOSTS" in
  dmit) TARGETS=(dmit) ;;
  la)   TARGETS=(la) ;;
  both) TARGETS=(dmit la) ;;
  *)    die "--hosts must be dmit, la or both (got '$HOSTS')" ;;
esac

for tool in ssh scp tar curl sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done
[[ -f "$SSH_KEY" ]] || die "SSH key not found: $SSH_KEY (use --key or \$SSH_KEY)"

SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o BatchMode=yes
          -o ConnectTimeout=25 -o ServerAliveInterval=10)
DMIT_SSH=(ssh "${SSH_OPTS[@]}" -i "$SSH_KEY" "root@$DMIT_HOST")

# Run a command on a host, whichever it is. LA is reached through DMIT.
# stdin is /dev/null: these run inside a `while read` loop in the health gate,
# and an ssh that inherits stdin would swallow the loop's input.
#
# Note the two different argument styles. ssh joins its own arguments with
# spaces, so for DMIT the words are passed through as-is. For LA the command is
# embedded in a second shell command, so each word must be escaped to survive
# that re-parse — escaping the DMIT case would turn the whole command into one
# word (which ssh would then try to execute as a single program name).
run_on() {
  local host="$1"; shift
  case "$host" in
    dmit) "${DMIT_SSH[@]}" "$@" </dev/null ;;
    la)   "${DMIT_SSH[@]}" \
            "ssh -o StrictHostKeyChecking=accept-new -o BatchMode=yes -o ConnectTimeout=20 -i $LA_JUMP_KEY root@$LA_HOST $(printf '%q ' "$@")" </dev/null ;;
  esac
}

# Copy a local file to a host.
put_to() {
  local host="$1" src="$2" dst="$3"
  case "$host" in
    dmit) scp "${SSH_OPTS[@]}" -i "$SSH_KEY" "$src" "root@$DMIT_HOST:$dst" >/dev/null </dev/null ;;
    la)   scp "${SSH_OPTS[@]}" -i "$SSH_KEY" "$src" "root@$DMIT_HOST:/tmp/deploy-hop" >/dev/null </dev/null
          "${DMIT_SSH[@]}" \
            "scp -o StrictHostKeyChecking=accept-new -o BatchMode=yes -i $LA_JUMP_KEY /tmp/deploy-hop root@$LA_HOST:$dst && rm -f /tmp/deploy-hop" </dev/null >/dev/null ;;
  esac
}

base_url()   { case "$1" in dmit) printf '%s' "$DMIT_BASE" ;; la) printf '%s' "$LA_BASE" ;; esac; }
base_path()  { case "$1" in dmit) printf '%s' "vpn-admin/assets/" ;; la) printf '%s' "Cul7KGMTQ8sEpXsfsN/assets/" ;; esac; }
# Assets in the embedded HTML are absolute paths (/vpn-admin/assets/… on DMIT,
# /Cul7KGMTQ8sEpXsfsN/assets/… on LA), so they resolve against the origin, not
# against the base path. Joining them onto the base path double-prefixes it.
origin_url() { case "$1" in dmit) printf '%s' "$DMIT_ORIGIN" ;; la) printf '%s' "$LA_ORIGIN" ;; esac; }
curl_extra() { case "$1" in dmit) printf '%s' "$DMIT_CURL_EXTRA" ;; la) printf '' ;; esac; }

# ----------------------------------------------------------------- health gate

# Three checks, none needing a credential (the sign-in page is unauthenticated).
# Check 3 is the one that would have caught the 2026-10-05 blank-page incident:
# a served index.html naming an asset that is not served alongside it.
health_gate() {
  local host="$1" base extra
  base="$(base_url "$host")"
  extra="$(curl_extra "$host")"
  local fail=0

  if run_on "$host" "systemctl is-active $SERVICE" | grep -qx active; then
    ok "check 1: $SERVICE active"
  else
    warn "check 1: $SERVICE is not active"; fail=1
  fi

  local raw code html
  raw="$(run_on "$host" "curl -sk $extra -o /dev/stdout -w '\n%{http_code}' '$base'" 2>/dev/null || true)"
  code="$(printf '%s' "$raw" | tail -n1 | tr -d '\r')"
  html="$(printf '%s' "$raw" | sed '$d')"

  if [[ "$code" == "200" ]] && printf '%s' "$html" | grep -qi '<html'; then
    ok "check 2: login page 200"
  else
    warn "check 2: $base returned '$code' (want 200 with an <html> body)"; fail=1
  fi

  # Check 3: the frontend must be self-consistent.
  #
  # This check exists because of the 2026-10-05 blank-page incident. The panel
  # served its login page and then rendered nothing, because the browser asked
  # for an asset the panel could not serve (the console showed a 404 on
  # assets/index-*.js). The embedded dist and its index.html were built at
  # different times and disagreed about the entry chunk's name.
  #
  # The unauthenticated login page cannot see this — the app shell lives behind
  # /panel/*, which 307-redirects without a session — so scraping only the served
  # HTML would miss exactly the file that went missing. Instead read the asset
  # names out of the embedded HTML inside the deployed binary, then fetch each
  # one and require 200 with a matching MIME type.
  #
  # MIME and not just status: a panel that falls back to serving index.html for
  # unknown paths answers 200 text/html for a missing .js, which would sail past a
  # status-only check and then fail in the browser as a module-parse error.
  #
  # The whole loop runs as ONE remote command. Doing it per-asset opens an ssh
  # connection per file (~37), which is slow and trips sshd's connection limits,
  # producing spurious 000s.
  if [[ "$code" == "200" ]]; then
    local report
    report="$(run_on "$host" "
      bin='$REMOTE_BIN'
      origin='$(origin_url "$host")'
      bpath='$(base_path "$host")'
      extra='$(curl_extra "$host")'
      total=0; bad=0
      for a in \$(strings -n 8 \"\$bin\" \
                 | grep -oE '(src|href)=\"[^\"]*assets/[^\"]+\"' \
                 | sed -E 's/.*=\"([^\"]*)\".*/\1/' | sed -E 's|.*/||' \
                 | grep -E '^[A-Za-z0-9][A-Za-z0-9_.-]*\.(js|css)\$' | sort -u); do
        total=\$((total+1))
        read c t <<EOF2
\$(curl -sk \$extra -o /dev/null -w '%{http_code} %{content_type}' \"\$origin/\$bpath\$a\")
EOF2
        case \"\$t\" in
          *javascript*|*css*) ;;
          *) echo \"MISS \$c \$t \$a\"; bad=\$((bad+1)) ;;
        esac
      done
      echo \"TOTAL \$total BAD \$bad\"
    " 2>/dev/null || true)"

    local total bad
    total="$(printf '%s' "$report" | sed -nE 's/.*TOTAL ([0-9]+) BAD [0-9]+.*/\1/p' | tail -n1)"
    bad="$(printf '%s' "$report" | sed -nE 's/.*TOTAL [0-9]+ BAD ([0-9]+).*/\1/p' | tail -n1)"

    if [[ -z "$total" || "$total" == "0" ]]; then
      warn "check 3: found no asset references in the embedded HTML (suspicious)"; fail=1
    elif [[ "${bad:-1}" != "0" ]]; then
      printf '%s\n' "$report" | grep '^MISS ' | while read -r _ c t a; do
        warn "check 3: embedded HTML references $a, served as '$c $t' (want 200 javascript/css)"
      done
      warn "check 3: $bad of $total referenced assets are not served correctly — the panel would render blank (this is the 2026-10-05 failure)"
      fail=1
    else
      ok "check 3: all $total assets referenced by the embedded HTML serve with a correct MIME type"
    fi
  fi

  return $fail
}

# --------------------------------------------------------------------- deploy

deploy_one() {
  local host="$1"
  step "Deploying to $host"

  local ts staging backup
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  staging="/root/x-ui.deploy-$ts"
  backup="/root/x-ui.bin.pre-deploy-$ts"

  local before
  before="$(run_on "$host" "sha256sum $REMOTE_BIN 2>/dev/null | cut -d' ' -f1" | tr -d '\r\n')"
  log "    deployed now : ${before:0:16}…"
  log "    release build: ${BIN_SHA:0:16}…"

  if [[ "$before" == "$BIN_SHA" ]]; then
    ok "already running this build — nothing to swap"
    return 0
  fi

  put_to "$host" "$WORKDIR/x-ui" "$staging"
  local staged
  staged="$(run_on "$host" "sha256sum $staging | cut -d' ' -f1" | tr -d '\r\n')"
  [[ "$staged" == "$BIN_SHA" ]] || die "$host: staged binary checksum mismatch — refusing to deploy"
  ok "staged; checksum matches the release binary"

  if [[ $DRY_RUN -eq 1 ]]; then
    warn "dry-run: $staging left on the host, binary NOT swapped"
    return 0
  fi

  run_on "$host" "set -e
    cp -a $REMOTE_BIN $backup
    chmod 755 $staging
    systemctl stop $SERVICE
    mv -f $staging $REMOTE_BIN
    systemctl start $SERVICE
    sleep 6" >/dev/null
  ok "swapped and restarted (rollback copy: $backup)"

  if health_gate "$host"; then
    local xr sg
    xr="$(run_on "$host" "pgrep -c -f 'xray-linux-amd64' || true" | tr -d '\r\n')"
    sg="$(run_on "$host" "pgrep -c -f 'sing-box run' || true" | tr -d '\r\n')"
    if [[ "${xr:-0}" -ge 1 && "${sg:-0}" -ge 1 ]]; then
      ok "children up: xray=$xr sing-box=$sg"
    else
      warn "children: xray=$xr sing-box=$sg (expected both >= 1)"
    fi
    ok "$host deploy complete"
    return 0
  fi

  warn "HEALTH GATE FAILED on $host — rolling back to the previous binary"
  run_on "$host" "set -e
    systemctl stop $SERVICE
    cp -a $backup $REMOTE_BIN
    chmod 755 $REMOTE_BIN
    systemctl start $SERVICE
    sleep 6" >/dev/null
  if health_gate "$host"; then
    warn "rolled back; $host is serving again. Backup kept at $backup"
  else
    die "$host: rollback did not restore health — needs a human. Backup: $backup"
  fi
  return 1
}

# ----------------------------------------------------------------------- main

if [[ $HEALTH_ONLY -eq 1 ]]; then
  rc=0
  for t in "${TARGETS[@]}"; do
    step "Health gate: $t"
    health_gate "$t" || rc=1
  done
  [[ $rc -eq 0 ]] && { log ""; log "${C_GREEN}${C_BOLD}All healthy.${C_RESET}"; exit 0; }
  die "one or more hosts failed the health gate"
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

command -v gh >/dev/null 2>&1 || die "gh CLI not found (used to fetch the release artifact)"

step "Resolving release $TAG"
gh release download "$TAG" --repo "$REPO" \
  --pattern "$ARTIFACT" --pattern "$ARTIFACT.sha256" \
  --dir "$WORKDIR" --clobber >/dev/null \
  || die "could not download $ARTIFACT from release $TAG"
ok "downloaded $ARTIFACT ($(du -h "$WORKDIR/$ARTIFACT" | cut -f1))"

step "Verifying the download"
TAR_SHA_WANT="$(cut -d' ' -f1 < "$WORKDIR/$ARTIFACT.sha256" | tr -d '\r\n')"
TAR_SHA_GOT="$(sha256sum "$WORKDIR/$ARTIFACT" | cut -d' ' -f1)"
[[ "$TAR_SHA_WANT" == "$TAR_SHA_GOT" ]] \
  || die "tarball checksum mismatch: release says ${TAR_SHA_WANT:0:16}…, file is ${TAR_SHA_GOT:0:16}…"
ok "tarball sha256 ${TAR_SHA_WANT:0:16}… (release and download agree)"

step "Extracting the binary"
# Only x-ui/x-ui. The tarball also carries bin/xray, geo databases and service
# files; overlaying those would replace the hand-built sing-box (which provides
# v2ray_api stats) and break traffic accounting. See the spec, Non-goals.
tar -xzOf "$WORKDIR/$ARTIFACT" x-ui/x-ui > "$WORKDIR/x-ui" \
  || die "could not extract x-ui/x-ui from the tarball"
chmod 755 "$WORKDIR/x-ui"
BIN_SHA="$(sha256sum "$WORKDIR/x-ui" | cut -d' ' -f1)"
ok "extracted x-ui ($(du -h "$WORKDIR/x-ui" | cut -f1)), binary sha256 ${BIN_SHA:0:16}…"

log ""
log "${C_BOLD}Release${C_RESET}  $TAG"
log "${C_BOLD}Targets${C_RESET}  ${TARGETS[*]}"
log "${C_BOLD}Mode${C_RESET}     $([[ $DRY_RUN -eq 1 ]] && echo 'dry-run (no swap)' || echo 'live')"
log "${C_YELLOW}Note:${C_RESET}     the restart disconnects clients for a few seconds."

rc=0
for t in "${TARGETS[@]}"; do
  deploy_one "$t" || rc=1
done

log ""
if [[ $rc -eq 0 ]]; then
  log "${C_GREEN}${C_BOLD}Deploy complete.${C_RESET}"
else
  die "deploy finished with failures (see above)"
fi
