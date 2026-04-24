pipeline {
  agent any

  options {
    timestamps()
    disableConcurrentBuilds()
    buildDiscarder(logRotator(numToKeepStr: "30"))
  }

  triggers {
    pollSCM("H/5 * * * *")
  }

  environment {
    GO_VERSION = "1.24.0"
  }

  stages {
    stage("Prepare Toolchain") {
      steps {
        sh '''#!/usr/bin/env bash
set -euo pipefail

host_arch="$(uname -m)"
case "$host_arch" in
  x86_64|amd64)
    GO_ARCH="amd64"
    ;;
  aarch64|arm64)
    GO_ARCH="arm64"
    ;;
  *)
    echo "Unsupported host arch: $host_arch" >&2
    exit 1
    ;;
esac

GO_ROOT="$HOME/toolcache/go-${GO_VERSION}-${GO_ARCH}"

if [ ! -x "$GO_ROOT/bin/go" ]; then
  mkdir -p "$(dirname "$GO_ROOT")"
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -o "$tmpdir/go.tgz"
  tar -C "$tmpdir" -xzf "$tmpdir/go.tgz"
  rm -rf "$GO_ROOT"
  mv "$tmpdir/go" "$GO_ROOT"
fi

"$GO_ROOT/bin/go" version
'''
      }
    }

    stage("Test") {
      steps {
        sh '''#!/usr/bin/env bash
set -euo pipefail

host_arch="$(uname -m)"
case "$host_arch" in
  x86_64|amd64)
    GO_ARCH="amd64"
    ;;
  aarch64|arm64)
    GO_ARCH="arm64"
    ;;
  *)
    echo "Unsupported host arch: $host_arch" >&2
    exit 1
    ;;
esac

export GOROOT="$HOME/toolcache/go-${GO_VERSION}-${GO_ARCH}"
export PATH="$GOROOT/bin:$PATH"
export GOTOOLCHAIN=local
export GOCACHE="$HOME/.cache/go-build"
export GOMODCACHE="$HOME/.cache/go-mod"
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"

go test ./...
'''
      }
    }

    stage("Build Release") {
      steps {
        sh '''#!/usr/bin/env bash
set -euo pipefail

host_arch="$(uname -m)"
case "$host_arch" in
  x86_64|amd64)
    GO_ARCH="amd64"
    ;;
  aarch64|arm64)
    GO_ARCH="arm64"
    ;;
  *)
    echo "Unsupported host arch: $host_arch" >&2
    exit 1
    ;;
esac

export GOROOT="$HOME/toolcache/go-${GO_VERSION}-${GO_ARCH}"
export PATH="$GOROOT/bin:$PATH"
export GOTOOLCHAIN=local
export GOCACHE="$HOME/.cache/go-build"
export GOMODCACHE="$HOME/.cache/go-mod"
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"

rm -rf dist
mkdir -p dist

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/infohub ./cmd/infohub
cp config.yaml dist/config.yaml
tar -C dist -czf dist/infohub-release.tgz infohub config.yaml
'''
      }
    }

    stage("Deploy To ECS") {
      steps {
        sh '''#!/usr/bin/env bash
set -euo pipefail

DEPLOY_HOST="${DEPLOY_HOST:?DEPLOY_HOST is required}"
DEPLOY_USER="${DEPLOY_USER:?DEPLOY_USER is required}"
DEPLOY_BASE="${DEPLOY_BASE:?DEPLOY_BASE is required}"
DEPLOY_SERVICE="${DEPLOY_SERVICE:?DEPLOY_SERVICE is required}"
DEPLOY_PORT="${DEPLOY_PORT:-22}"
raw_key_path="${SSH_KEY_PATH:-$HOME/.ssh/collect_server_ecs}"
SSH_KEY_REAL="${raw_key_path/#~/$HOME}"
chmod 600 "$SSH_KEY_REAL"
mkdir -p "$HOME/.ssh"
ssh-keyscan -p "${DEPLOY_PORT}" -H "${DEPLOY_HOST}" >> "$HOME/.ssh/known_hosts" 2>/dev/null || true

RELEASE_ID="$(date +%Y%m%d%H%M%S)-b${BUILD_NUMBER}"
REMOTE="${DEPLOY_USER}@${DEPLOY_HOST}"
REMOTE_ARCHIVE="/tmp/infohub-${RELEASE_ID}.tgz"

scp -i "$SSH_KEY_REAL" -P "${DEPLOY_PORT}" -o BatchMode=yes dist/infohub-release.tgz "${REMOTE}:${REMOTE_ARCHIVE}"

ssh -i "$SSH_KEY_REAL" -p "${DEPLOY_PORT}" -o BatchMode=yes "${REMOTE}" bash -s -- "${RELEASE_ID}" "${DEPLOY_BASE}" "${DEPLOY_SERVICE}" "${REMOTE_ARCHIVE}" <<'REMOTE_SCRIPT'
set -euo pipefail

release_id="$1"
deploy_base="$2"
service_name="$3"
archive_path="$4"
release_root="${deploy_base}/releases/${release_id}"
app_dir="${release_root}/app"

mkdir -p "${app_dir}"
tar -xzf "${archive_path}" -C "${app_dir}"
cp "${deploy_base}/current/.env" "${app_dir}/.env"
chown -R infohub:infohub "${release_root}"
ln -sfn "${app_dir}" "${deploy_base}/current"
systemctl restart "${service_name}"
systemctl is-active "${service_name}"
rm -f "${archive_path}"
REMOTE_SCRIPT
'''
      }
    }

    stage("Health Check") {
      steps {
        sh '''#!/usr/bin/env bash
set -euo pipefail

DEPLOY_HOST="${DEPLOY_HOST:?DEPLOY_HOST is required}"
DEPLOY_USER="${DEPLOY_USER:?DEPLOY_USER is required}"
DEPLOY_PORT="${DEPLOY_PORT:-22}"
raw_key_path="${SSH_KEY_PATH:-$HOME/.ssh/collect_server_ecs}"
SSH_KEY_REAL="${raw_key_path/#~/$HOME}"
REMOTE="${DEPLOY_USER}@${DEPLOY_HOST}"

ssh -i "$SSH_KEY_REAL" -p "${DEPLOY_PORT}" -o BatchMode=yes "${REMOTE}" <<'REMOTE_CHECK'
set -euo pipefail
cd /opt/infohub/current
set -a
. ./.env
set +a
health_url="http://127.0.0.1:${INFOHUB_PORT:-8080}/api/v1/health"
for attempt in $(seq 1 30); do
  if curl -fsS -H "Authorization: Bearer ${INFOHUB_AUTH_TOKEN}" "${health_url}"; then
    exit 0
  fi
  echo "Health check attempt ${attempt} failed, retrying..." >&2
  sleep 1
done

echo "Health check failed after 30 attempts: ${health_url}" >&2
exit 1
REMOTE_CHECK
'''
      }
    }
  }
}
