Source demos/vhs/config.tape
Output demos/vhs/generated/api/http-api.gif

Hide
Type "bash demos/setup-flowstate-demo.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 300ms
Show

Type "# Start the FlowState HTTP server"
Enter
Sleep 500ms
Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "./build/flowstate serve &"
Enter
Sleep 2s

Type "# List agents via API"
Enter
Sleep 300ms
Type "curl -s localhost:8080/api/agents | python3 -m json.tool"
Enter
Sleep 2s

Type "# Discover agents via API"
Enter
Sleep 300ms
Type "curl -s 'localhost:8080/api/discover?message=write+tests' | python3 -m json.tool"
Enter
Sleep 2s

Type "kill %1"
Enter
Sleep 500ms
