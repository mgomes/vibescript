- **Fixed: a zone abbreviation was silently treated as UTC.** Applying the
  zoneless UTC default to an input naming a zone made Go fabricate the
  abbreviation at offset zero, so `Mon, 27 Jul 2026 14:30:45 EDT` shifted by
  four hours. Such inputs resolve against the host's zone database again; a
  timestamp naming no zone still defaults to UTC on every host.
