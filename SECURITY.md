# Security Policy

## Supported versions

Go Workflow is pre-1.0 and under active development. Security fixes are applied to the
latest `main` and the most recent tagged release.

## Reporting a vulnerability

Please **do not** open a public issue for security vulnerabilities.

Instead, report privately using GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository, or email the maintainer at **ehabterra@gmail.com** with:

- a description of the issue and its impact,
- steps to reproduce (a minimal workflow definition or code sample if possible),
- any suggested remediation.

You can expect an acknowledgement within a few days. We'll work with you on a fix and a
coordinated disclosure timeline, and credit you in the release notes unless you prefer to
remain anonymous.

## Scope notes

- Guard expressions are evaluated with [expr-lang/expr](https://github.com/expr-lang/expr),
  which is sandboxed (side-effect-free, always-terminating). Report anything that appears
  to break those guarantees.
- Storage backends execute SQL built from developer-supplied table/column configuration.
  Treat that configuration as trusted; report any path where untrusted *workflow data*
  can influence SQL structure.
