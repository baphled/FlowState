#!/bin/bash
set -e
FAKE_HOME="$(pwd)/demos/temp_demo_env"
rm -rf "$FAKE_HOME"
mkdir -p "$FAKE_HOME/.flowstate/sessions"

# Copy sample agents and skills
cp -r "$(pwd)/agents" "$FAKE_HOME/.flowstate/agents"
cp -r "$(pwd)/skills" "$FAKE_HOME/.flowstate/skills"

# Create a minimal config
cat > "$FAKE_HOME/.flowstate/config.yaml" << 'EOF'
agents_dir: ~/.flowstate/agents
skills_dir: ~/.flowstate/skills
sessions_dir: ~/.flowstate/sessions
default_provider: ollama
EOF

echo "Demo environment ready at $FAKE_HOME"
