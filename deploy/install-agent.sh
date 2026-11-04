#!/usr/bin/env bash
# 闲存云 Agent（xiancun-agent）一键部署脚本（Linux systemd）
#
# 功能：
#   1. 安装二进制（本地路径或 URL）
#   2. 写入 Agent 配置模板（如未提供，需指定控制面地址）
#   3. 创建专用运行用户与目录
#   4. 安装并启用 systemd 服务，开机自启
#   5. 状态确认
#
# 用法：
#   sudo ./install-agent.sh --binary ./xiancun-agent --server http://server.example.com:8080
#   sudo ./install-agent.sh --binary https://example.com/xiancun-agent-linux-amd64 --server http://127.0.0.1:8080
#   sudo ./install-agent.sh --binary ./xiancun-agent --config /path/to/agent-config.json
#
# 环境变量（可选）：
#   XIANCUN_USER   运行用户（默认 xiancun）
#   XIANCUN_DIR    安装根目录（默认 /opt/xiancun）
set -euo pipefail

# ---------- 默认值 ----------
BINARY_SRC="${1:-}"
SERVER_URL=""
CONFIG_SRC=""
INSTALL_USER="${XIANCUN_USER:-xiancun}"
INSTALL_DIR="${XIANCUN_DIR:-/opt/xiancun}"
BIN_PATH="/usr/local/bin/xiancun-agent"
CONFIG_PATH="/etc/xiancun/agent-config.json"
UNIT_NAME="xiancun-agent.service"
UNIT_PATH="/etc/systemd/system/${UNIT_NAME}"

log()  { printf '[install] %s\n' "$*"; }
die()  { printf '[install] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

# ---------- 参数解析 ----------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary) BINARY_SRC="${2:-}"; shift 2 ;;
    --server) SERVER_URL="${2:-}"; shift 2 ;;
    --config) CONFIG_SRC="${2:-}"; shift 2 ;;
    -h|--help) usage ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

# ---------- 前置检查 ----------
[[ $EUID -eq 0 ]] || die "please run as root (sudo)"
command -v systemctl >/dev/null 2>&1 || die "systemctl not found (not a systemd system?)"

# ---------- 1. 安装二进制 ----------
if [[ -n "$BINARY_SRC" ]]; then
  TMP_BIN="$(mktemp)"
  trap 'rm -f "$TMP_BIN"' EXIT
  if [[ "$BINARY_SRC" =~ ^https?:// ]]; then
    log "downloading binary from $BINARY_SRC"
    curl -fsSL "$BINARY_SRC" -o "$TMP_BIN" || die "download failed"
  else
    log "using local binary $BINARY_SRC"
    cp "$BINARY_SRC" "$TMP_BIN"
  fi
  chmod +x "$TMP_BIN"
  install -m 0755 "$TMP_BIN" "$BIN_PATH"
  log "installed binary -> $BIN_PATH"
else
  [[ -x "$BIN_PATH" ]] || die "no binary at $BIN_PATH; pass --binary <path|url>"
  log "using existing binary $BIN_PATH"
fi

# ---------- 2. 运行用户与目录 ----------
id "$INSTALL_USER" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$INSTALL_USER"
mkdir -p "$INSTALL_DIR" /etc/xiancun
chown -R "$INSTALL_USER:$INSTALL_USER" "$INSTALL_DIR"

# ---------- 3. 配置文件 ----------
if [[ -n "$CONFIG_SRC" ]]; then
  install -m 0640 -o "$INSTALL_USER" -g "$INSTALL_USER" "$CONFIG_SRC" "$CONFIG_PATH"
  log "installed config -> $CONFIG_PATH"
elif [[ ! -f "$CONFIG_PATH" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -f "$SCRIPT_DIR/agent-config.json.example" ]]; then
    cp "$SCRIPT_DIR/agent-config.json.example" "$CONFIG_PATH"
    # 替换控制面地址（模板默认 127.0.0.1:8080）。
    if [[ -n "$SERVER_URL" ]]; then
      sed -i "s|\"server\"[[:space:]]*:[[:space:]]*\"[^\"]*\"|\"server\": \"${SERVER_URL}\"|" "$CONFIG_PATH"
    else
      die "no --server <control-plane-url> provided and no config given; e.g. --server http://server.example.com:8080"
    fi
    chown "$INSTALL_USER:$INSTALL_USER" "$CONFIG_PATH"
    chmod 0640 "$CONFIG_PATH"
    log "wrote config -> $CONFIG_PATH (server=${SERVER_URL})"
  else
    die "no config provided and no template found"
  fi
else
  log "using existing config $CONFIG_PATH"
fi

# ---------- 4. systemd 单元 ----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ ! -f "$SCRIPT_DIR/xiancun-agent.service" ]]; then
  die "xiancun-agent.service not found next to this script"
fi
sed -e "s|^User=.*|User=${INSTALL_USER}|" \
    -e "s|^Group=.*|Group=${INSTALL_USER}|" \
    -e "s|^WorkingDirectory=.*|WorkingDirectory=${INSTALL_DIR}|" \
    -e "s|^ExecStart=.*|ExecStart=${BIN_PATH} -config ${CONFIG_PATH}|" \
    "$SCRIPT_DIR/xiancun-agent.service" > "$UNIT_PATH"
log "installed unit -> $UNIT_PATH"

# ---------- 5. 启动并设置开机自启 ----------
systemctl daemon-reload
systemctl enable --now "$UNIT_NAME"
log "service enabled and started"

# ---------- 6. 状态确认 ----------
sleep 3
systemctl --no-pager status "$UNIT_NAME" --lines=5 || true
log "done. useful commands:"
log "  systemctl status ${UNIT_NAME}    # 服务状态"
log "  journalctl -u ${UNIT_NAME} -f    # 实时日志（心跳/KV 监听信息）"
log "  systemctl disable --now ${UNIT_NAME}  # 卸载开机自启"
