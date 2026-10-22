# Contributing

## Getting started

```
git clone https://github.com/mxreal64/jv
cd jv
make build          # builds ./jv
make test           # go test -race ./...
./test-all.sh       # full end-to-end suite (requires javac and Maven)
./test-all.sh --quick  # just unit tests + static checks, no network
```

Go 1.22 or later is required. The binary is purely static (`CGO_ENABLED=0`).

## Repository layout

```
cli.go            main entry point and flag parser
cli_gradle.go     Gradle model-oracle build flow
main.go           version wiring
pom/              POM parser, property interpolation, profile activation
resolver/         dependency resolution, version comparison, lock file, downloader
compiler/         javac wrapper, resource copying
runner/           JUnit Platform launcher
packager/         jar builder
config/           settings.xml (mirrors, credentials, proxies, profiles)
bridge/           delegation to mvn / Gradle
gradle/           Gradle model oracle (init script, JSON model, cache)
jdk/              JDK version detection
```

## Making a change

1. Fork, create a branch, make the change.
2. Add or update tests. The unit tests in each package cover the relevant logic; the
   integration scenarios in `test-all.sh` cover end-to-end behaviour.
3. Run `make fmt && make test` and `./test-all.sh --quick`. If Maven is available, run the
   full suite to keep classpath parity with `mvn`.
4. Open a pull request with a clear description of what changed and why.

## The parity rule

The correctness bar for the resolver is: `jv classpath <scope>` must produce the same set
of jars as `mvn dependency:build-classpath -Dclassifier=<scope>`, for every scope, on every
project in the corpus. If a change breaks parity on any real project, it is a bug.
`test-all.sh --full` runs the corpus check.

## Design constraints

- **No JVM, no CGO.** The binary must be a single static Go executable.
- **Fail safe.** Anything jv does not implement must delegate to real `mvn`/Gradle, not fail
  silently or produce a wrong result.
- **No new flags that mvn doesn't have.** jv is a drop-in accelerator; user muscle memory must
  transfer.

## Reporting bugs

Open a GitHub issue with the `jv` version (`jv --version`), your OS and Java version, and
the minimal `pom.xml` or project structure that reproduces the problem. For security issues,
use the private vulnerability report instead (see `SECURITY.md`).
