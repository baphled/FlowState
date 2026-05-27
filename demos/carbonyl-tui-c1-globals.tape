# Verifies the C1 carbonyl CSS overlay (global theme variable overrides +
# universal resets). With body.carbonyl-mode active, all CSS custom
# properties are forced to pure black backgrounds with matching colours
# and 1px borders are widened to 2px so Carbonyl rasterises clean cells
# instead of half-block (▄) artefacts.
#
# Compare against demos/carbonyl-tui-focus-fix.{gif,txt} which is the
# pre-C1 baseline showing dense half-block stippling.

Output demos/carbonyl-tui-c1-globals.gif
Output demos/carbonyl-tui-c1-globals.txt

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
Type "C1VARSANDRESETS"
Sleep 3s

# Verify deletion still works through the focused composer.
Backspace 5
Sleep 2s

# Exit carbonyl cleanly.
Ctrl+C
Sleep 1s
