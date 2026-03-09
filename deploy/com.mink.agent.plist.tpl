<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.mink.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>MINK_BIN</string>
        <string>serve</string>
    </array>
    <key>WorkingDirectory</key>
    <string>MINK_WORKDIR</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>MINK_HOME</string>
        <key>MINK_LOG_LEVEL</key>
        <string>info</string>
    </dict>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>MINK_LOG_DIR/mink.out.log</string>
    <key>StandardErrorPath</key>
    <string>MINK_LOG_DIR/mink.err.log</string>
</dict>
</plist>
