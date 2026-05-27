# Verifies the keyboard-focus fix for the Carbonyl-rendered FlowState TUI.
# Goal: confirm that with `?carbonyl=1` in the URL, the Vue SPA auto-focuses
# the composer textarea on mount so typing lands in the input without a Tab.
#
# Compare against demos/carbonyl-tui-broken.gif which showed zero state
# change because nothing had focus.

Output demos/carbonyl-tui-focus-fix.gif
Output demos/carbonyl-tui-focus-fix.txt

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

# Wait for SPA mount AND carbonyl first stable frame.
Sleep 6s

# No Tab, no click. Type a distinctive string that should land in the
# auto-focused composer textarea if the fix works.
Type "KEYBOARDFOCUSWORKS123"
Sleep 3s

# Verify deletion works through the focused composer.
Backspace 5
Sleep 2s

# Exit carbonyl cleanly.
Ctrl+C
Sleep 1s
