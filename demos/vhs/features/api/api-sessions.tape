Source demos/vhs/config.tape
Output demos/vhs/generated/api/api-sessions.gif

Hide
Type "bash demos/setup-flowstate-demo.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 300ms
Show

Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "# Start server"
Enter
Sleep 300ms
Type "./build/flowstate serve &"
Enter
Sleep 2s

Type "# Sessions endpoint (empty initially)"
Enter
Type "curl -s localhost:8080/api/sessions"
Enter
Sleep 1s

Type "# API also serves agents, skills, and discover"
Enter
Sleep 300ms
Type "curl -s localhost:8080/api/agents | python3 -m json.tool | head -10"
Enter
Sleep 1s

Type "kill %1"
Enter
Sleep 500ms
