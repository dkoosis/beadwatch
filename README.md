# beadwatch

beadwatch derives the shared bead-count cache (`counts.json`) that the Claude
Code status line and every other reader consume, off any render path. It
replaces strand's `strand counts` subcommand as the one writer of that file.
