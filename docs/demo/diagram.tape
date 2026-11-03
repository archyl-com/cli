# archyl diagram — C4 views in the terminal, then what the branch touches.
# Re-record: see README.md in this directory.

Output diagram.gif

Require archyl

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 1000
Set Padding 20
Set Framerate 15
Set TypingSpeed 40ms
Set CursorBlink false
Set Theme { "name": "archyl", "background": "#09090b", "foreground": "#fafafa", "cursor": "#a78bfa", "selection": "#27272a", "black": "#18181b", "red": "#f87171", "green": "#4ade80", "yellow": "#fbbf24", "blue": "#60a5fa", "magenta": "#a78bfa", "cyan": "#22d3ee", "white": "#d4d4d8", "brightBlack": "#52525b", "brightRed": "#fca5a5", "brightGreen": "#86efac", "brightYellow": "#fde68a", "brightBlue": "#93c5fd", "brightMagenta": "#c4b5fd", "brightCyan": "#67e8f9", "brightWhite": "#ffffff" }

Hide
Type "source $ARCHYL_DEMO_ENV && cd $ARCHYL_DEMO_REPO && export PS1='$ ' && clear"
Enter
Sleep 500ms
Show

# The system context, then the components of one container.
Type "archyl diagram"
Sleep 300ms
Enter
Sleep 2s

Type "archyl diagram Backend --inside"
Sleep 300ms
Enter
Sleep 3.5s

Hide
Type "clear"
Enter
Sleep 300ms
Show

# What the branch touches, drawn: touched elements in heavy boxes.
Type "archyl impact --diagram"
Sleep 300ms
Enter
Sleep 5s
