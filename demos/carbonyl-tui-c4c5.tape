# Verifies the C4 (MessageBubble chrome) + C5 (modals/sidebars/overlays)
# carbonyl CSS chunks layered on top of C1+C2+C3. With body.carbonyl-mode
# active:
#   - User bubbles render with a 2px cyan border (var(--accent))
#   - Assistant bubbles render with a 2px white border (var(--border))
#   - Bubbles have no rounded corners and no drop shadow
#   - Modals/overlays render with a 2px white frame, solid black bg,
#     opaque backdrop (no translucent blur)
#
# Compare against demos/carbonyl-tui-c2c3.{gif,txt} which is the post-C3
# baseline (NavBar + composer chunks shipped, but bubble + modal chrome
# still using the rounded/shadowed/translucent web defaults).

Output demos/carbonyl-tui-c4c5.gif
Output demos/carbonyl-tui-c4c5.txt

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

# Launch the chat TUI — resumes the previous session so existing user +
# assistant bubbles render immediately, giving us the C4 test surface.
Type "./flowstate chat"
Enter

# Session restore + carbonyl rasterisation needs longer than a cold mount.
Sleep 8s

# Distinctive marker — confirms focus + keyboard still work with the new
# bubble + modal CSS overlay in place.
Type "C4C5FINAL"
Sleep 2s

# Deselect anything, then open the keyboard help modal so we capture the
# C5 chrome (2px white frame, solid black bg, opaque backdrop).
Escape
Type "?"
Sleep 5s

# Close the modal and return to the chat view.
Escape
Sleep 3s

# Verify backspace still travels through the focused composer.
Backspace 5
Sleep 1s

# Exit carbonyl cleanly.
Ctrl+C
Sleep 1s
