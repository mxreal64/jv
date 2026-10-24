<h1 align="center">jv</h1>

<p align="center">
  <b>Maven and Gradle builds, minus the wait.</b><br>
  One small static binary that resolves, compiles, tests and packages your project &mdash;<br>
  and hands anything it can't reproduce exactly back to the real tool.
</p>

<p align="center">
  <a href="https://github.com/mxreal64/jv/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/mxreal64/jv/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/mxreal64/jv/releases"><img alt="Release" src="https://img.shields.io/github/v/release/mxreal64/jv?include_prereleases&sort=semver"></a>
  <a href="LICENSE"><img alt="License: GPL-3.0-only" src="https://img.shields.io/badge/license-GPL--3.0--only-blue.svg"></a>
  <img alt="Go 1.22+" src="https://img.shields.io/badge/go-1.22%2B-00ADD8.svg">
</p>

```console
$ cd my-project            # any Maven or Gradle project, untouched
$ jv test                  # resolve, compile, run your JUnit tests
$ jv package               # target/my-project-1.0.jar (or spring-boot / shaded fat jar)
```

## Why jv

- **Drop-in.** It reads the `pom.xml` or Gradle build you already have. No config, no migration, no
  lock-in: delete `jv` and nothing about your project has changed. It shares `~/.m2` with Maven.
- **Never silently different.** When a build uses plugins or configurations jv doesn't reproduce natively,
  jv tells you why and runs the real `mvn` or `gradle` instead. You get the same result, just not the speed-up.
- **Checked against Maven.** The test suite diffs jv's compile, runtime and test classpaths with
  `mvn dependency:build-classpath` on real dependency graphs (Spring Boot, Hadoop, Hibernate, Netty,
  AWS SDK, Maven core, Jackson, HttpClient, Log4j, and more).
- **Fast where it counts.**
  - **Parallel downloads** with checksum verification.
  - **Parallel reactor builds** for multi-module projects.
  - **Persistent javac worker** daemon eliminating JVM cold-start latency.
  - **ABI fingerprinting** to skip downstream rebuilds when public method signatures haven't changed.
  - **Cached resolutions** (`.jv/lock.json`) and content-addressable caching (CAS), with optional distributed caching (`jv-cache`).
  - **Static Gradle Kotlin DSL parsing** (`ktsparse`) for instant zero-JVM compilation of Gradle projects, plus the Gradle model oracle for advanced builds.
- **Native Popular Plugins.** Built-in native support for Spring Boot executable repackaging, Maven Shade fat-jars (with relocation and resource merging), Flatten Maven plugin, resource filtering with property interpolation, JaCoCo, and Enforcer.
- **Developer Experience.** Dedicated commands for project initialization (`jv init`), dependency security audits (`jv audit`), version upgrades (`jv upgrade`), and dependency path visualization (`jv explain`).
- **One static binary.** Linux, macOS and Windows on amd64 and arm64. The only thing it needs is a JDK.
- **Free software.** GPL-3.0 (Copyright mxreal64, 2026).

## Install

**Download a release.** Grab the archive for your platform from the
[Releases page](https://github.com/mxreal64/jv/releases), extract it, and put `jv` on your `PATH`
(Windows: unzip and add the folder to `PATH`). Every release includes `checksums.txt`:

```sh
sha256sum -c checksums.txt --ignore-missing      # Linux
shasum -a 256 jv_*_darwin_*.tar.gz               # macOS: compare the hash with checksums.txt
```

**Or build from source** (Go 1.22+):

```sh
go install github.com/mxreal64/jv@latest
```

You need a JDK (`JAVA_HOME` or `PATH`). Maven or Gradle only has to be installed for the builds jv
delegates to them. Check it worked with `jv --version`.

## Try it

```sh
jv test              # resolve, compile and run JUnit tests
jv package           # test, then build target/<artifactId>-<version>.jar (or fat-jar)
jv install           # ...and install it into ~/.m2
jv classpath test    # print the test classpath (IDEs, scripts, debugging)
jv tree              # display the resolved dependency tree
jv -pl app test      # one module of a multi-module build, plus required siblings
jv clean install     # goals run in the order you type them
jv audit             # scan dependencies for known CVE vulnerabilities
jv upgrade           # check for newer versions of dependencies and wrappers
jv -X test           # verbose logging: say whether build ran natively or was delegated
```

Want it for every build? `alias mvn=jv` in your shell: anything jv doesn't handle goes to the real
`mvn`. (Use an alias, not a symlink.)

## How it decides

```
jv <goal>
 |
 |-- pom.xml ----------> can jv reproduce this build natively?
 |                         |-- yes --> native: resolve -> javac -> JUnit -> jar/repackage
 |                         '-- no  --> "delegating to mvn because ..." (your build, unchanged)
 |
 '-- build.gradle[.kts] -> static fast-path parser (ktsparse) or single Gradle model oracle query
                           |-- reproducible --> native parallel compilation, testing & packaging
                           '-- otherwise    --> real Gradle execution, arguments translated
```

jv prefers a loud hand-off to a quiet difference. See *Maven projects* and *Gradle projects* below for
details on what runs natively and what delegates.

## Usage reference

```
jv compile | test | package | install | clean
jv classpath [compile|runtime|test]     jv tree [query]
jv init | audit | upgrade | explain <dep>
jv -o -q -X -U -P profile -Dkey=value -pl module1,module2 -s settings.xml <goal>
jv --version | -h
```

`jv` automatically selects the build system: a `pom.xml` means Maven; otherwise a Gradle build
(`build.gradle[.kts]`, `settings.gradle[.kts]` or `gradlew`) means Gradle.

## Maven projects

### Done natively
- **Dependency resolution** with full Maven semantics: parent POMs, `${}` interpolation (including inside
  property values), `dependencyManagement` (with spec-compliant exclusion handling), BOM imports, exclusions,
  optional dependencies, scope mediation (widened scopes widen transitive children), nearest-wins conflicts,
  version ranges, and exact port of Maven's `ComparableVersion` ordering.
- **Profiles**: `-P`/`-P!id`, `activeByDefault`, and activation by property, `<jdk>`, `<os>` and `<file>`;
  modules declared inside profiles are integrated into the reactor.
- **Remote `-SNAPSHOT` dependencies** (unique and non-unique layouts); `-U` re-checks them.
- **Multi-module reactors** in dependency topological order; siblings resolve to their build output. `-pl a,b`
  builds the named modules plus required siblings (with parallel execution support).
- **Resource filtering & syncing**: Property substitution (`${property}`) in non-binary resources, and automatic
  tracking/removal of deleted resources from `src/*/resources` in the output directory.
- **Plugin support natively**:
  - `spring-boot-maven-plugin`: `repackage` goal creates executable fat-jars with `BOOT-INF/classes`,
    `BOOT-INF/lib`, and Spring Boot launcher manifests.
  - `maven-shade-plugin`: Fat-jar assembly, package relocation, and resource transformers (appending `META-INF/services`, etc.).
  - `flatten-maven-plugin`: Generates clean flattened POMs (`.flattened-pom.xml`) for publication.
  - `maven-enforcer-plugin` & `jacoco-maven-plugin`: Basic verification and execution hooks.
  - Subprocess plugin API: Extensible JSON-RPC protocol for external plugin integration.
- **Configuration & Workspace**:
  - `jv.toml`: Project-level configuration to override compiler options, JVM arguments, and flags.
  - `jv-workspace.toml`: Workspace configuration for multi-repo environments.
- **`clean` in any position**: Leading `clean` uses a zero-network fast path; `clean` anywhere else runs at its
  scheduled position in the goal sequence.
- **`~/.m2/settings.xml`**: Mirrors, credentials, `localRepository`, active profiles, proxies, and SHA-1 verification.
  Encrypted server passwords (`{...}`) are decrypted using `~/.m2/settings-security.xml`.
- **Compiler & Testing**:
  - Persistent `javac` worker daemon for instant compilation.
  - ABI fingerprinting to prevent unnecessary rebuilds across modules.
  - `maven-compiler-plugin` `<release>`, `<source>`, `<target>`, `<encoding>`, and compiler arguments.
  - Annotation processor classpath support on modern JDKs (`-proc:full`).
  - `maven-surefire-plugin`: Ant glob includes/excludes, JUnit tag filtering (`groups`/`excludedGroups`),
    `-Dtest=...` filtering, `<argLine>`, and `<systemPropertyVariables>`. XML test reports generated in `target/surefire-reports`.
- **Flags**: `-DskipTests`, `-Dmaven.test.skip`, `-Dtest=...`, `-Dmaven.test.failure.ignore=true`, `-o` (offline),
  `-q` (quiet), `-X` (debug/verbose).

### Delegated to `mvn`
- Specialized plugins with custom life-cycle bindings not handled natively.
- Packagings other than `jar` and `pom` (e.g. `war`, `ear`, `maven-plugin`).
- Complex custom plugin executions outside supported plugin goals.

## Gradle projects

- **Static Kotlin DSL Analyzer (`ktsparse`)**:
  Fast, native static parser for `build.gradle.kts` files that extracts dependencies, plugins, and source sets
  instantly without starting a JVM or Gradle daemon.
- **Model Oracle**:
  For complex builds, queries Gradle once (using wrapper if present) for a complete model, caches it in
  `.jv/gradle-model.json` keyed by build scripts and properties, and compiles/tests natively on subsequent runs.
- **Native Execution**: `clean`, `compile`, `test`, `package`, and `classpath`.
- **Bridge Translation**: Delegated goals are translated accurately (`compile`→`classes`, `test`→`test`,
  `package`→`build`, `install`→`publishToMavenLocal`, `tree`→`dependencies`, `-DskipTests`→`-x test`).
- **Safety**: Safe path guards prevent accidental deletion of source directories during clean.

## Developer Experience Commands

- **`jv init`**: Scaffolds a new Java project or creates a `jv.toml` configuration in an existing project.
- **`jv audit`**: Scans resolved project dependencies against known vulnerability advisories.
- **`jv upgrade`**: Checks for newer versions of dependencies, plugins, and build wrappers.
- **`jv explain <dep>`**: Traces and visualizes why a specific dependency is present in the dependency tree.

## Caching & Performance

- **Persistent Javac Worker**: Reuses in-memory compiler instances to eliminate JVM startup overhead.
- **ABI Fingerprinting**: Analyzes class signatures to avoid recompiling downstream modules when only private implementation details change.
- **Content-Addressable Cache (CAS)**: Deduplicates dependencies and build artifacts locally.
- **Distributed Cache Server (`jv-cache`)**: Run `jv-cache -addr :8080` to share build artifacts across machines and CI agents.

## Environment Variables

| Variable | Meaning |
|---|---|
| `JV_REPO` | Local Maven repository directory (overrides `settings.xml`) |
| `JV_JUNIT_VERSION` | JUnit platform standalone launcher version (default `1.10.2`) |
| `JV_IGNORE_BUILD_CONFIG` | Build natively even if unsupported plugins or settings are present |
| `JV_GRADLE_ORACLE` | `0` = disable native Gradle oracle; always delegate to Gradle CLI |
| `JV_REQUIRE_CHECKSUMS` | Refuse artifacts that do not have `.sha1` checksums |
| `JAVA_HOME` | JDK used for `javac`/`java` execution |

## Testing

Run the comprehensive test suite with `./test-all.sh` (see `--help`):

| Tier | Description |
|---|---|
| `--quick` | Unit tests, static analysis (`gofmt`, `go vet`), and cross-compilation across platforms. |
| default | Quick tests plus end-to-end integration scenarios, Maven parity checks, reactor builds, and Gradle stub/real scenarios. |
| `--full` | Complete suite including heavy dependency graphs (Hadoop, Hibernate, Netty, AWS SDK) and real open-source corpus parity. |
| `--corpus-only` | Parity checks on real open-source repositories (`commons-lang`, `commons-io`, `jackson`, `gson`, etc.). |

## Contributing

Bug reports and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Please run
`./test-all.sh --quick` before submitting changes.

## License

jv is free software, licensed under the **GNU General Public License, version 3 only**
(SPDX: `GPL-3.0-only`); see [LICENSE](LICENSE). Copyright mxreal64, 2026. Every source file
carries an SPDX header. Contributions are accepted under the same license.
