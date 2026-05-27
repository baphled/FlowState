# Captures the broken Carbonyl-rendered TUI for `flowstate chat`.
# Goal: produce visual evidence of UI rendering issues in the
# Chromium-in-terminal chat surface so we can triage what's broken.

Output demos/carbonyl-tui-broken.gif
Output demos/carbonyl-tui-broken.txt

Set Shell "bash"
Set FontSize 12
Set FontFamily "JetBrains Mono"
# Carbonyl renders Chromium pixels as terminal glyphs, so it benefits
# from a generous viewport. 200x50 cells gives the SPA room to lay out.
Set Width 1600
Set Height 900
Set Theme "Catppuccin Mocha"
Set TypingSpeed 50ms
Set Framerate 30

# Make carbonyl discoverable (installed under nvm).
Env PATH "/home/baphled/.config/nvm/versions/node/v25.6.0/bin:/home/baphled/.nix-profile/bin:/usr/local/bin:/usr/bin:/bin"

Hide
Type "cd /home/baphled/Projects/FlowState.git/feature-terminal-carbonyl"
Enter
Sleep 500ms
Type "clear"
Enter
Sleep 300ms
Show

# Launch the chat TUI (carbonyl renders the Vue SPA from web/dist).
Type "./flowstate chat"
Enter

# Initial Chromium boot + first paint can take several seconds.
Sleep 12s

# Exercise interactive elements: Tab through focusable controls,
# then arrow keys / Page navigation to surface any redraw glitches.
Tab
Sleep 800ms
Tab
Sleep 800ms
Tab
Sleep 800ms
Tab
Sleep 800ms

Down
Sleep 500ms
Down
Sleep 500ms
Up
Sleep 500ms

# Try typing into whatever has focus (likely the prompt input)
# to see how character entry renders in carbonyl.
Type "hello flowstate"
Sleep 2s

# Hold on a final frame so the still capture is legible.
Sleep 8s

# Exit carbonyl cleanly.
Ctrl+C
Sleep 1s
