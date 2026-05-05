# Initial requirements - vibecopd

## Summary

Claude, Deepseek, and I have implemented an extension to "Open Vibe Island" to
enable a different AI to approve/escalate permissions checks via hooks to cut
down on human interrupts.

That's available in ~/src/open-vibe-island/

The best way to fully familiarize yourself with what the change does is to read
the spec: ~/src/open-vibe-island/docs/vibecop/README.md

This is cool but I'd like to have some alternative ways to do this:
- This is a pretty unknown, largely Chinese codebase.  I can't run this at work.
- Would be kinda cool to not really need the UI at all and just have a more subtle program
  providing this option.

## Goal
- A new golang program that:
    - Has no fancy GUI, but can run in the background (vibecop*d*) 
    - Can also run in the foreground in a nice modern TUI
        - When doing this, provides plenty of information about what is happening
    - Implements the AI second-opinion flow
    - Reimplements the open-island hooks, installation of said hooks
    - Duplicates the audit logging we added there and otherwise logs important information
    - Supports the same model matrix and API combos
    - Treats particularly Gemini, Claude, and Deepseek as first-class models and coding harnesses
    - Provides the same Guardian/Baseline initialization options somehow
    - May use a config directory (~/.vibecopd) for global options and per-project files
    - Provides model performance/latency reporting
    - Has informational interaction with coding harness permission requests
    - "escalate" must clearly fall back to the coding harness user interface since we have no GUI
    - Duplicates the open-island fail-open semantics
