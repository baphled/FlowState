Source demos/vhs/config.tape
Output demos/vhs/generated/cli/agent-list.gif

Hide
Type "bash demos/setup-flowstate-demo.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 300ms
Show

Type "# List all available agents"
Enter
Sleep 500ms
Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "./build/flowstate agent list"
Enter
Sleep 2s

Type "# Get details for a specific agent"
Enter
Sleep 500ms
Type "./build/flowstate agent info general"
Enter
Sleep 2s
