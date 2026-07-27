- **Fixed: `vibes check` reported one copy of a body diagnostic per call site.**
  A helper called eight times produced eight identical lines for a single
  mistake -- same file, line, column, and text -- which buried the other genuine
  findings and made "check failed with N issue(s)" count call sites rather than
  problems. Diagnostics identical in every field are now reported once.
