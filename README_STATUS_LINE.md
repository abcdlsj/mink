# Custom Status Line

Mink 支持通过 bash 脚本自定义底部 status line。

## 配置

在 `~/.config/mink/config.toml` 中添加：

```toml
status_line = "/path/to/your/status-script.sh"
```

或者直接使用内联脚本：

```toml
status_line = "git branch --show-current 2>/dev/null | sed 's/^/git:/' || echo ''"
```

## 脚本要求

- 脚本必须在 500ms 内返回
- 输出应该简短（建议 < 50 字符）
- 输出会自动 trim 空白字符
- 如果脚本失败或超时，status line 不显示自定义内容

## 示例脚本

### Git 分支

```bash
#!/bin/bash
if git rev-parse --git-dir > /dev/null 2>&1; then
    branch=$(git branch --show-current 2>/dev/null)
    [ -n "$branch" ] && echo "git:$branch"
fi
```

### 系统负载

```bash
#!/bin/bash
uptime | awk -F'load average:' '{print $2}' | cut -d',' -f1 | xargs | sed 's/^/load:/'
```

### 组合信息

```bash
#!/bin/bash
info=""

# Git branch
if git rev-parse --git-dir > /dev/null 2>&1; then
    branch=$(git branch --show-current 2>/dev/null)
    [ -n "$branch" ] && info="git:$branch"
fi

# Kubernetes context
if command -v kubectl &> /dev/null; then
    ctx=$(kubectl config current-context 2>/dev/null)
    [ -n "$ctx" ] && info="$info | k8s:$ctx"
fi

echo "$info"
```

## UI 改进

- 更简洁的 header（去掉多余的 divider）
- 优化配色，更接近 Claude Code 风格
- Footer 采用深色背景，更清晰的层次感
- 自定义 status line 通过 `│` 分隔符与默认信息分开
