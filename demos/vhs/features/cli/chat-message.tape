Source demos/vhs/config.tape
Output demos/vhs/generated/cli/chat-message.gif

Hide
Type "bash demos/setup-flowstate-demo.sh"
Enter
Sleep 1s
Type "clear"
Enter
Sleep 300ms
Show

Type "# Chat with --message flag"
Enter
Sleep 500ms
Type "export HOME=$(pwd)/demos/temp_demo_env"
Enter
Type "./build/flowstate chat --message 'What is Go programming language?' --agent general"
Enter
Sleep 4s
