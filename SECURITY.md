# Security policy

Please report vulnerabilities privately (GitHub "Report a vulnerability" on this repository) rather
than in a public issue. Include the jv version (`jv --version`), your OS, and a POM or
`settings.xml` that reproduces the problem.

Areas that matter most: artifact download and verification, credential handling for repositories
(`settings.xml` servers and mirrors), and anything that executes processes (`javac`, `java`, `mvn`,
`gradle`).

Hardening switches: `JV_REQUIRE_CHECKSUMS=1` refuses any artifact whose repository provides no `.sha1`.
