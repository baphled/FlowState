# Verifies the C2 (NavBar) + C3 (MessageInput composer / buttons /
# icon-to-text fallback) carbonyl CSS chunks layered on top of C1.
# With body.carbonyl-mode active, NavBar inherits the flattened theme,
# composer buttons render as solid text-labelled cells, and SVG icons
# fall back to text glyphs so Carbonyl can rasterise them cleanly.
#
# Compare against demos/carbonyl-tui-c1-globals.{gif,txt} which is the
# post-C1 baseline.

Output demos/carbonyl-tui-c2c3.gif
Output demos/carbonyl-tui-c2c3.txt

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

# Distinctive marker — confirms focus + keyboard still work with the
# new CSS overlay in place.
Type "C2C3ICONSBUTTONS"
Sleep 3s

# Verify deletion still works through the focused composer.
Backspace 5
Sleep 2s

# Exit carbonyl cleanly.
Ctrl+C
Sleep 1s
