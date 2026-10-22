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

Type "# Discover the best agent for implementing a feature"
Enter
Sleep 500ms
Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "./build/flowstate discover 'implement a feature'"
Enter
Sleep 2s

Type "# Discover the best agent for research"
Enter
Sleep 500ms
Type "./build/flowstate discover 'research and investigate'"
Enter
Sleep 2s
