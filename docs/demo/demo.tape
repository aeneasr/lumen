Output ../../../docs/demo/demo.gif

Set FontSize 13
Set Width 1200
Set Height 700
Set Theme "Dracula"
Set PlaybackSpeed 1.0
Set TypingSpeed 10ms

# Prerequisite (run once before recording to pre-index):
#   ./bin/lumen index testdata/fixtures/go

Type "claude --dangerously-skip-permissions"
Enter
Sleep 2s

Type "How does TSDB compaction work end-to-end? Explain the Compactor interface, LeveledCompactor, and how the DB triggers compaction. Show relevant types, interfaces, and key method signatures."
Enter
Sleep 20s
