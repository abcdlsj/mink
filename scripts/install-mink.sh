#!/bin/bash
set -e

ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    ARCH="amd64"
elif [ "$ARCH" = "aarch64" ]; then
    ARCH="arm64"
fi

TAG=${1:-latest}
BIN_DIR="/usr/local/bin"
SERVICE_FILE="/etc/systemd/system/mink.service"

echo "Installing mink $TAG for $ARCH..."

sudo wget -q -O "$BIN_DIR/mink" "https://github.com/abcdlsj/mink/releases/download/$TAG/$ARCH"
sudo chmod +x "$BIN_DIR/mink"

echo "Installing systemd service..."
sudo tee "$SERVICE_FILE" > /dev/null <<EOF
[Unit]
Description=Mink AI Agent
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/mink serve
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable mink
sudo systemctl restart mink
sudo systemctl status mink

echo "Done!"
