# archyl map — the full-screen C4 explorer, as a tree and as a diagram.
# Re-record: see README.md in this directory.

Output map.gif

Require archyl

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 860
Set Padding 20
Set Framerate 20
Set TypingSpeed 40ms
Set CursorBlink false
Set Theme { "name": "archyl", "background": "#09090b", "foreground": "#fafafa", "cursor": "#a78bfa", "selection": "#27272a", "black": "#18181b", "red": "#f87171", "green": "#4ade80", "yellow": "#fbbf24", "blue": "#60a5fa", "magenta": "#a78bfa", "cyan": "#22d3ee", "white": "#d4d4d8", "brightBlack": "#52525b", "brightRed": "#fca5a5", "brightGreen": "#86efac", "brightYellow": "#fde68a", "brightBlue": "#93c5fd", "brightMagenta": "#c4b5fd", "brightCyan": "#67e8f9", "brightWhite": "#ffffff" }

Hide
Type "source $ARCHYL_DEMO_ENV && cd $ARCHYL_DEMO_REPO && export PS1='$ ' && clear"
Enter
Sleep 500ms
Show

Type "archyl map"
Sleep 300ms
Enter
Sleep 2s

# Open the system, then switch to its diagram.
Right
Sleep 700ms
Down
Sleep 600ms
Type "d"
Sleep 1.8s

# Tab between the boxes, enter to drill into a container.
Tab
Sleep 800ms
Tab
Sleep 800ms
Enter
Sleep 2s
Tab
Sleep 700ms
Tab
Sleep 1.2s

# Back to the tree, on the selected element.
Type "d"
Sleep 2.2s

Type "q"
Sleep 500ms
