Source demos/vhs/config.tape
Output demos/vhs/generated/cli/discover.gif

Hide
Type "bash demos/setup-flowstate-demo.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 300ms
Show

Type "# Discover the best agent for writing tests"
Enter
Sleep 500ms
Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "./build/flowstate discover 'write unit tests for my module'"
Enter
Sleep 2s

Type "# Discover the best agent for research"
Enter
Sleep 500ms
Type "./build/flowstate discover 'research and investigate this topic'"
Enter
Sleep 2s
