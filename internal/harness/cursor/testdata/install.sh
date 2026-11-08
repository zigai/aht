#!/bin/sh
set -eu
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/cursor-agent" <<'EOF'
#!/bin/sh
echo ready
EOF
chmod 700 "$HOME/.local/bin/cursor-agent"
