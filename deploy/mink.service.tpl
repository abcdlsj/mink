[Unit]
Description=Mink AI Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=MINK_BIN serve
Restart=on-failure
RestartSec=3
WorkingDirectory=MINK_WORKDIR
Environment=HOME=MINK_HOME
Environment=MINK_LOG_LEVEL=info

[Install]
WantedBy=default.target
