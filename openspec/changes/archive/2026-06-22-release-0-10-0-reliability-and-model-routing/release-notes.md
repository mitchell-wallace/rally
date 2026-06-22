
# Release Notes: 0.10.0

This release focuses on improving reliability, routing semantics, and CLI visibility.

## Historical Incident IDs

This release addresses several historical incidents. These incidents are now tracked in New Relic, and the following historical Sentry references are provided for context:

- **General Reliability:** RALLY-2, RALLY-3, RALLY-4, RALLY-6, RALLY-8, RALLY-9, RALLY-B, RALLY-C
- **OpenCode Reliability:** RALLY-Q, RALLY-K, RALLY-D

## Release Checklist Verification

The following items have been verified for this release:

- No alert regression for routine rate-limit categories.
- Corrected run header text.
- Muted cancelled output.
- `rally tail` defaults to active output.
