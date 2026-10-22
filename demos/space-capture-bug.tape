Output demos/space-capture-bug.gif
Set Theme "Catppuccin Mocha"
Set FontSize 12
Set Width 1200
Set Height 600

Type "flowstate chat"
Enter
Sleep 3s

# Enter insert mode
Type "i"
Sleep 1s

# Type "hello" with visible pauses
Type "h"
Sleep 100ms
Type "e"
Sleep 100ms
Type "l"
Sleep 100ms
Type "l"
Sleep 100ms
Type "o"
Sleep 500ms

# Type space - this is where the bug manifests
Type " "
Sleep 1s

# Type "world" with visible pauses
Type "w"
Sleep 100ms
Type "o"
Sleep 100ms
Type "r"
Sleep 100ms
Type "l"
Sleep 100ms
Type "d"
Sleep 2s

# Show the footer clearly - space should be missing
Sleep 2s

# Exit insert mode
Escape
Sleep 1s

# Exit app
Type "q"
Enter
Sleep 1s
