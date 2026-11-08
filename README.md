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
$ jv package               # target/my-project-1.0.jar
```

> **Status:** pre-1.0 (release candidates). The core build paths are covered by tests, but try jv on a branch
> before relying on it in CI. See [Known limitations](#known-limitations) and [Roadmap](#roadmap).

## Why jv

- **Drop-in.** It reads the `pom.xml` or Gradle build you already have. No config, no migration, no
  lock-in: delete `jv` and nothing about your project has changed. It shares `~/.m2` with Maven.
- **Never silently different.** When a build uses something jv can't reproduce exactly (plugin executions,
  filtered resources, custom source directories, ...), jv tells you why and runs the real `mvn` or `gradle`
  instead. You get the same result, just not the speed-up.
- **Checked against Maven.** The test suite diffs jv's compile, runtime and test classpaths with
  `mvn dependency:build-classpath` on real dependency graphs (Spring Boot, Hadoop, Hibernate, Netty,
  AWS SDK, Maven core, Jackson, HttpClient, Log4j, and more).
- **Fast where it counts.**
  - Parallel, checksum-verified downloads.
  - Independent modules compile in parallel, in dependency order (`JV_JOBS` sets the worker count).
  - A cached dependency resolution (`.jv/lock.json`).
  - `javac` is skipped when nothing changed, and a module that depends on another is only rebuilt when
    that module's *public interface* changed, not when just a method body did.
  - An optional shared build cache for downloaded artifacts (`JV_CACHE_URL`, see [Shared cache](#shared-cache)).
  - For Gradle, a single model query that is cached until your build scripts change.
  - An optional persistent `javac` worker (experimental, off by default; see [Environment](#environment)).
- **Dependency tooling built in.** `jv explain` (why is this on my classpath?), `jv audit` (known
  vulnerabilities, via OSV) and `jv upgrade` (newer releases), plus `jv init` to start a project (see
  [Dependency tools](#dependency-tools)).
- **Editor-friendly.** `jv lsp` is a small JSON-lines server that reports a project's classpath, source
  roots and coordinates (see [Editor integration](#editor-integration)).
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
jv package           # test, then build target/<artifactId>-<version>.jar
jv install           # ...and install it into ~/.m2
jv classpath test    # print the test classpath (IDEs, scripts, debugging)
jv tree              # print the resolved dependency tree
jv explain guava     # why is guava on the classpath, and who asked for which version?
jv audit             # check the dependencies against the OSV vulnerability database
jv upgrade           # which dependencies have a newer release?
jv init              # scaffold a new Maven project in the current (empty) directory
jv -pl app test      # one module of a multi-module build, plus the siblings it needs
jv clean install     # goals run in the order you type them
jv -X test           # verbose: also say whether the build ran natively or was delegated
```

Want it for every build? `alias mvn=jv` in your shell: anything jv doesn't handle goes to the real
`mvn`. (Use an alias, not a symlink.)

## How it decides

```
jv <goal>
 |
 |-- pom.xml ----------> can jv reproduce this build exactly?
 |                         |-- yes --> native: resolve -> javac -> JUnit -> jar
 |                         '-- no  --> "delegating to mvn because ..."   (your build, unchanged)
 |
 '-- build.gradle[.kts] -> ask Gradle once for a model (cached in .jv/)
                           |-- reproducible --> the same native steps
                           '-- otherwise    --> Gradle itself, arguments translated
```

jv prefers a loud hand-off to a quiet difference. See *Maven projects* and *Gradle projects* below for
exactly what runs natively and what is delegated.

## Usage reference

```
jv compile | test | package | install | clean
jv classpath [compile|runtime|test]     jv tree     jv explain <dependency>
jv audit [-Daudit.failOn=none|low|medium|high|critical] [-Daudit.json=true]
jv upgrade [-Dupgrade.all=true] [-Dupgrade.json=true]
jv init [-DgroupId=g] [-DartifactId=a] [-Dversion=v] [-Dtype=spring-boot]
jv lsp
jv -o -q -X -U -P profile -Dkey=value -pl module1,module2 -s settings.xml <goal>
jv --version | -h
```

`jv` picks the build system for you: a `pom.xml` means Maven; otherwise a Gradle build
(`build.gradle[.kts]`, `settings.gradle[.kts]` or `gradlew`, in the directory or an ancestor) means Gradle.
Any other goal, plugin or flag is passed to the real tool untouched.

## Maven projects

### Done natively

- **Dependency resolution** with Maven semantics: parent POMs, `${}` interpolation (including inside
  property values), `dependencyManagement`, BOM imports, exclusions, optional dependencies, scope mediation
  (a widened scope also widens the transitive children), nearest-wins conflicts, version ranges, and an
  exact port of Maven's `ComparableVersion` ordering.
- **Profiles**: `-P`/`-P!id`, `activeByDefault`, and activation by property, `<jdk>`, `<os>` and `<file>`;
  modules declared inside profiles are part of the reactor.
- **Remote `-SNAPSHOT` dependencies** (unique and non-unique layouts); `-U` re-checks them.
- **Multi-module reactors**: independent modules compile in parallel (one worker per CPU, or `JV_JOBS`),
  in dependency order. Tests, packaging and `install` then run one module at a time, like Maven, so tests
  of different modules never overlap. Siblings resolve to their build output. `-pl a,b` builds the named modules plus the
  siblings they need (so `-am` is accepted and ignored) and `clean` with `-pl` cleans only those modules.
- **`clean` in any position**: a leading `clean` takes a fast path that needs no network or POM resolution; a
  `clean` anywhere else (`jv install clean`) runs at its position in the goal list.
- **`~/.m2/settings.xml`**: mirrors, credentials, `localRepository`, `<profiles>` (repositories and
  properties), `<proxies>`; POM-declared repositories; SHA-1 verification. Encrypted server passwords
  (`{...}`) are decrypted with the master password in `~/.m2/settings-security.xml`.
- **Build steps**: resource copying that also removes resources you deleted from `src/*/resources`, skip-if-
  unchanged `javac`, JUnit Platform tests (the launcher is fetched automatically; XML reports go to
  `target/surefire-reports`), jars with directory entries, and `install` into the local repository.
- **Per-project overrides** in a `jv.toml` ([see below](#project-settings-jvtoml)).
- **Compiler settings**: `maven.compiler.*` properties and `maven-compiler-plugin` `<release>`, `<source>`,
  `<target>`, `<encoding>`. Annotation processors on the classpath keep working on javac 23+ (jv passes
  `-proc:full` on javac 21+).
- **Plugin management**: `<pluginManagement>` entries for `maven-compiler-plugin`, `maven-surefire-plugin`
  and `maven-jar-plugin` are read, because Maven applies them to the default lifecycle without the plugin
  being declared.
- **Test selection (`maven-surefire-plugin`)**: `<includes>`/`<excludes>` (Ant globs such as `**/*IT.java`),
  `<groups>`/`<excludedGroups>` (JUnit tags), `<argLine>` and `<systemPropertyVariables>`. `-Dtest=...`
  overrides includes/excludes, as in Surefire.
- **Flags**: `-DskipTests`, `-Dmaven.test.skip`, `-Dtest=...`, `-Dmaven.test.failure.ignore=true`, `-o`
  (offline), `-q` (quiet), `-X` (verbose). `-B -e -ntp -V -am` are accepted and ignored.

### Delegated to `mvn`

- Plugins and any goal that isn't in the list above; packagings other than `jar`/`pom` (war, ear,
  maven-plugin, ...); projects without a readable `pom.xml`.
- Any flag jv doesn't know (`-T`, `-f`, ...).
- POMs, including inherited parent POMs, that customise the build in ways jv doesn't reproduce by default:
  - `sourceDirectory` / `testSourceDirectory`, `finalName`;
  - non-default or filtered `resources` / `testResources`;
  - compiler configuration other than release/source/target/encoding (`compilerArgs`,
    `annotationProcessorPaths`, ...);
  - surefire configuration beyond the list above (`forkCount`, `parallel`, ...), `%regex[...]` / `%ant[...]`
    test patterns, and an `<argLine>` that uses `@{...}` late binding (jacoco);
  - **any plugin `<executions>`** (shade, spring-boot repackage, flatten, jacoco, enforcer, ...).

  jv prints which setting caused the delegation.

### Experimental native plugins (opt-in)

jv contains native implementations for a few common plugins: `spring-boot-maven-plugin` (`repackage`),
`maven-shade-plugin` (fat jars, relocation, resource merging), `flatten-maven-plugin`,
`maven-enforcer-plugin`, `jacoco-maven-plugin`, plus resource filtering with property interpolation and
generator hooks. Because they are not yet checked against Maven as thoroughly as the core build, builds that
declare them are **delegated to `mvn` by default**.

To run them natively, set `JV_IGNORE_BUILD_CONFIG=1`. jv then builds natively even when the POM has settings
it doesn't reproduce, and prints a warning naming them:

```sh
JV_IGNORE_BUILD_CONFIG=1 jv package
```

Compare the result with `mvn package` before trusting it, especially for fat jars.

## Gradle projects

jv does not re-implement the Groovy/Kotlin DSL. It asks a real Gradle (the wrapper if present) **once** for a
JSON model through an init script (source dirs, resolved compile/runtime/test classpaths, javac and test
settings), caches it in `.jv/gradle-model.json` keyed by the build scripts, `gradle.properties`, version
catalogs and `-P/-D` arguments, then compiles, tests and packages natively. Later runs do not start Gradle at
all until those inputs change.

- **Native**: `clean`, `compile`, `test`, `package` (like `build`: it runs the tests), `classpath`.
  `classpath` names exactly one project (`-pl`, or the project you are in).
- **Through Gradle** (the bridge): everything else, and any build the model flags as not reproducible
  (Kotlin/Groovy/Scala, war, annotation processors, several source dirs, TestNG, extra tasks wired into
  `compileJava`, non-core plugins, a project-level dependency cycle). `JV_GRADLE_ORACLE=0` always uses the
  bridge; `JV_IGNORE_BUILD_CONFIG=1` builds natively despite the warnings.
- **Bridge translation**: `compile`→`classes`, `test`→`test`, `package`→`build`, `install`→`publishToMavenLocal`,
  `tree`→`dependencies`, `-DskipTests`→`-x test`, `-o`→`--offline`, `-X`→`--info`,
  `-pl a,b`→`:a:<task> :b:<task>`; `-s settings.xml` is dropped (it has no Gradle equivalent). Gradle's exit
  code is returned unchanged.
- **Safety**: `jv clean` refuses to delete a build directory that is, or contains, the project or its sources
  (a script can set `buildDir = projectDir`); nothing is deleted if any target looks wrong.

## Dependency tools

These read the resolved dependency graph of a Maven project (they need a `pom.xml`) and never build anything.

**`jv explain <dependency>`** shows every path to a dependency in the resolved tree, then everything that
declares it and which version each one asked for. That makes version conflicts that Maven's "nearest wins"
rule hides easy to spot. The argument is `artifactId`, `groupId:artifactId` or `groupId:artifactId:version`.

```console
$ jv explain libc
demo:app:1.0
  org.acme:liba:1.0 -> org.acme:libc:1.0
  declared by:
    org.acme:liba:1.0 wants 1.0
    org.acme:libb:1.0 wants 2.0
```

**`jv audit`** looks every resolved artifact up in the [OSV](https://osv.dev) database and prints findings
by severity (CVSS v3 scores are computed when an advisory has no label). It exits `0` when clean, `1` when
there are findings and `2` when the audit itself could not run (offline, API down), so a failed audit never
looks like a clean one. `-Daudit.failOn=high` only fails on HIGH or CRITICAL, `-Daudit.failOn=none` never
fails, `-Daudit.json=true` prints JSON. `JV_OSV_URL` points at a mirror. `-SNAPSHOT` and unversioned artifacts
are skipped.

**`jv upgrade`** lists direct dependencies that have a newer release, from the repositories'
`maven-metadata.xml`: `org.acme:liba 1.0 -> 2.0.0 (major)`. Pre-releases are not offered unless you are on
one, `-jre`/`-android` style variants are kept, and artifacts that are in no repository are skipped.
`-Dupgrade.all=true` includes transitive dependencies. It only reports; it never edits your POM.

**`jv init`** creates `pom.xml`, a sample class and test, and a `.gitignore` in the current directory
(`-DartifactId` defaults to the directory name). It refuses to run if a `pom.xml` or Gradle build is already
there and never overwrites anything.

## Project settings: jv.toml

An optional `jv.toml` next to a module's `pom.xml` overrides POM settings for jv without touching the POM:

```toml
[build]
release = 21            # javac --release (also: source, encoding)

[test]
includes = ["**/*Test.java", "**/*IT.java"]
excludes = ["**/Slow*.java"]
groups = "fast"         # JUnit tags to run     (also: excludedGroups)
argLine = "-Xmx1g"      # JVM arguments for the test run
```

Settings in `jv.toml` win over the POM, and `-D` on the command line wins over both. Unknown keys produce a
warning (so typos don't pass silently) and a syntax error stops the build with the line number. `jv.toml` is
ignored when a build is delegated to `mvn` (jv says so), and it does not apply to Gradle projects.

### Code generators

A `[[generator]]` runs a command that writes Java sources; its output directory is compiled together with
the module's own sources. The command runs in the module directory, `${property}` references and glob
arguments such as `src/main/proto/*.proto` are expanded, and the generator is skipped when its command and
`inputs` have not changed. A generator that fails stops the build.

```toml
[[generator]]
id = "proto"                              # letters, digits, . _ -
phase = "generate-sources"                # or generate-test-sources (the default is generate-sources)
command = ["protoc", "--java_out=target/gen", "src/main/proto/*.proto"]
output = "target/gen"                     # added as a source root
inputs = ["src/main/proto/**/*.proto"]    # re-run when these change
```

### Plugins

A `[[plugin]]` runs an installed executable at a lifecycle phase. jv writes one JSON line describing the module
(directories, classpaths, properties) and your `config` table to its stdin, and reads one JSON line back:
`{"sourceRoots": [...], "properties": {...}, "skip": false}`. Exit code 2 means "nothing to do". The plugin
runs in the module directory; relative source roots are resolved against it. The plugin is skipped when its
binary, `config`, phase and `inputs` have not changed, and its last source roots and properties are replayed
so generated sources are never lost.

```toml
[[plugin]]
id = "org.acme:schema-gen"                # groupId:artifactId
version = "1.2"
phase = "generate-sources"
inputs = ["schema/*.json"]

[plugin.config]
package = "com.acme.model"
```

jv does not download plugins. Put the executable at `~/.jv/plugins/org.acme/schema-gen/1.2/plugin`
(`plugin.exe` on Windows; set `JV_PLUGIN_DIR` to use another root). A declared plugin that is not installed
fails the build and names the path it looked for. Plugins are programs you install and trust: they run with
your permissions.

## Shared cache

Set `JV_CACHE_URL` to share downloaded artifacts between machines or CI jobs through a `jv-cache` server:

```sh
jv-cache -dir /var/cache/jv -addr :8080 -token s3cret        # on the cache host
JV_CACHE_URL=http://cache.internal:8080 JV_CACHE_TOKEN=s3cret jv test
```

jv asks the cache before downloading an artifact and uploads what it downloaded. The cache can't poison a
build: entries are keyed by the SHA-256 of the checksum the origin repository publishes, and a cached file is
only used when its own SHA-1 matches that checksum (otherwise jv downloads it normally). Artifacts without a
published checksum are never shared. A cache that is down or slow only costs a download. Use `https` (or a
trusted network): the token and traffic are not protected over plain `http`, and jv warns when you do.

## Editor integration

`jv lsp` runs a lightweight JSON-RPC 2.0 server over stdin/stdout for editors (Neovim, VS Code, Helix, Zed, Emacs) and scripts, supporting both Maven and Gradle projects. It reads one JSON request per line and writes one JSON response per line:

```console
$ echo '{"jsonrpc":"2.0","id":1,"method":"projectInfo"}' | jv lsp
{"jsonrpc":"2.0","id":1,"result":{"buildSystem":"maven","group":"demo","artifact":"hello","version":"1.0","modules":[{"group":"demo","artifact":"hello","version":"1.0","dir":"/work/hello"}]}}
```

| Method | Params | Result |
|---|---|---|
| `classpath` | `{"file": "/path/to/File.java", "scope": "compile" \| "runtime" \| "test", "module": "app"}` | `{"classpath": [...], "scope": "...", "module": "...", "release": "...", "outputDir": "..."}` |
| `compilerOptions` | `{"file": "/path/to/File.java", "module": "app"}` | `{"release": "17", "source": "17", "target": "17", "encoding": "UTF-8", "compilerArgs": [...], "mainOutput": "...", "testOutput": "..."}` |
| `sourceRoots` | `{"file": "...", "module": "..."}` | `{"main": [...], "test": [...], "resources": [...], "testResources": [...]}` |
| `projectInfo` | none | `{"buildSystem": "maven" \| "gradle", "group": "...", "artifact": "...", "version": "...", "modules": [...]}` |

- **File-scoped queries**: passing a `file` path automatically detects the owning module and auto-detects whether it is a `test` or `compile` file, returning the exact classpath, release version, and compiler options needed for that file.
- **Auto-invalidation**: Resolution is cached and refreshed automatically when build scripts (`pom.xml`, `jv.toml`, `build.gradle*`, `settings.gradle*`, `gradle.properties`) change.

## Repositories and security

- Credentials from `settings.xml` are only sent to Maven Central, settings mirrors, and repositories declared
  in the project's own POMs, never to repositories that only a dependency's POM declared.
- Plain-`http` repositories declared in POMs are ignored (as in Maven 3.8.1+); `localhost` is allowed and an
  `http` mirror configured in `settings.xml` is honoured.
- Downloads are verified against the repository's `.sha1` when it provides one (`-X` shows artifacts that
  couldn't be verified). That detects corruption, not a malicious repository; `JV_REQUIRE_CHECKSUMS=1`
  refuses artifacts without a checksum.
- **Encrypted passwords**: a server `<password>` of the form `{...}` is decrypted with the master password
  from `~/.m2/settings-security.xml` (itself encrypted, as written by `mvn --encrypt-master-password`). If
  decryption fails at build time jv warns once and sends **no** credentials rather than a wrong password. The
  decrypted password lives in memory only and is never logged.

## Environment

| Variable | Meaning |
|---|---|
| `JV_REPO` | local repository (overrides `settings.xml`; relative paths are resolved at startup) |
| `JV_JUNIT_VERSION` | `junit-platform-console-standalone` version to fetch (default `1.10.2`) |
| `JV_IGNORE_BUILD_CONFIG` | build natively even if the POM/Gradle model has settings jv doesn't reproduce (also enables the [experimental plugins](#experimental-native-plugins-opt-in)) |
| `JV_GRADLE_ORACLE` | `0` = never use the native Gradle path, always run Gradle itself |
| `JV_REQUIRE_CHECKSUMS` | refuse artifacts whose repository provides no `.sha1` |
| `JV_PLUGIN_DIR` | root directory of installed [plugins](#plugins) (default `~/.jv/plugins`) |
| `JV_JOBS` | number of modules compiled at once in a multi-module build (default: one per CPU) |
| `JV_CACHE_URL` / `JV_CACHE_TOKEN` | [shared cache](#shared-cache) server and its bearer token |
| `JV_OSV_URL` | OSV API base URL used by `jv audit` (default `https://api.osv.dev`) |
| `JV_JAVAC_WORKER` | `1` = compile through a persistent `javac` worker process (experimental, off by default; it helps repeated compiles in one session and costs a JVM start otherwise; falls back to plain `javac` if it can't start) |
| `JAVA_HOME` | JDK used for `javac`/`java` (wins over `PATH`, like Maven and Gradle) |

## Known limitations

- `-f`/`--file` is not handled natively (it is delegated to `mvn`); a non-default POM is only supported as a
  `<module>` entry.
- Password decryption reads `~/.m2/settings-security.xml` only and covers server passwords, not proxy
  passwords.
- Surefire: only Ant-style `includes`/`excludes`; a pattern without a leading `**/` matches the default
  package only (as in Surefire). An `<argLine>` that still contains `${...}` after interpolation drops those
  tokens.
- Compilation is whole-module, not per-class.
- Independent modules compile in parallel, so compiler output and log lines of different modules can
  interleave; tests, packaging and `install` run one module at a time in dependency order (like Maven).
  Results are the same for successful builds; failure behaviour can differ slightly.
- TestNG is not supported natively; test discovery uses JUnit Platform engines found on the classpath.
- Classpath order follows Maven's tree order but is not guaranteed identical in every edge case (the set of
  jars is).

## Roadmap

- Running experimental plugins natively by default, backed by comprehensive Maven and Gradle parity test suites.
- Continued performance optimizations across incremental compilation and worker daemons.

## Benchmarking

`./bench.sh` times `jv` against `mvn`, `mvnd` and Gradle on a generated project or one of your own:

```sh
./bench.sh --jv ./jv --runs 7 --warmup 2 --out bench-results.md
./bench.sh --project path/to/your/project --no-gradle
```

### Measured Performance (Linux x86_64, OpenJDK 27, Median of 5 Runs)

| Scenario | `jv` | `mvn` 3.10 | `mvnd` 1.0.2 | `gradle` 9.9 | Notes |
| :--- | :---: | :---: | :---: | :---: | :--- |
| **Warm: Nothing Changed** | **10 ms** | 3,357 ms | 138 ms | 7,194 ms | Pure startup + stamp check (**~335x faster than Maven**) |
| **Cold Resolve + Compile** | **2,360 ms** | 10,648 ms | 4,975 ms | 9,486 ms | Empty repo, full network download + javac (**~4.5x faster**) |
| **Warm: One File Changed** | **1,061 ms** | 3,535 ms | 145 ms | 6,360 ms | Incremental compile on modified source (**~3.3x faster**) |
| **Warm: Lock Miss** | **856 ms** | — | — | — | Classes wiped, local repo cached → re-resolve & compile |
| **Full Build** | **3,841 ms** | 7,028 ms | 863 ms | 6,167 ms | Full lifecycle: compile + test + package (**~1.8x faster**) |

*Note: For Gradle, `--no-daemon` is used to measure cold tool startup; warm Gradle daemon performance is comparable to `mvnd`.*

Speed-ups depend on project structure, dependency tree depth, and plugin configurations. Use `./bench.sh` to measure on your own repositories.

## Testing

`./test-all.sh` is the single entry point (see `--help`):

| Tier | What runs |
|---|---|
| `--quick` | gofmt, `go vet`, `go test -race`, cross-compiles for linux/darwin/windows (no network) |
| default | quick + end-to-end scenarios against Maven Central: incremental builds, tests passing/failing, scopes, exclusions, profiles, reactor builds, `install`, offline mode, delegation, and Gradle (with a stub `gradle`, plus a real-Gradle run when Gradle is installed) |
| `--full` | default + heavier dependency graphs (hadoop, hibernate, netty, aws sdk) + a corpus of real open-source projects |
| `--corpus-only` | just the corpus |

If `mvn` is installed, every classpath check also diffs `jv` against `mvn dependency:build-classpath` for the
compile, runtime and test scopes, and the corpus compares compile output with Maven's. That parity check is
the real definition of "correct" for this tool. `--corpus-file FILE` takes
`name|git url|ref|subdir|cp|build` lines.

## Contributing

Bug reports and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Run
`./test-all.sh --quick` before sending a change, and `./test-all.sh` for anything that affects resolution or
builds. Report security problems privately (see [SECURITY.md](SECURITY.md)).

## License

jv is free software, licensed under the **GNU General Public License, version 3 only**
(SPDX: `GPL-3.0-only`); see [LICENSE](LICENSE). Copyright mxreal64, 2026. Every source file
carries an SPDX header. Contributions are accepted under the same license.
