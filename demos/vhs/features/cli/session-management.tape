Source demos/vhs/config.tape
Output demos/vhs/generated/cli/session-management.gif

Hide
Type "bash demos/setup-flowstate-demo.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 300ms
Show

Type "# List sessions (empty initially)"
Enter
Sleep 300ms
Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "./build/flowstate session list"
Enter
Sleep 1s

Type "# Try to resume a non-existent session"
Enter
Sleep 300ms
Type "./build/flowstate session resume my-session"
Enter
Sleep 1s
