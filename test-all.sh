#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
# Copyright (C) 2026 jv contributors
# test-all.sh - comprehensive tests for jv.
#
#   ./test-all.sh                 static checks + unit tests + end-to-end scenarios (+ Maven parity if mvn exists)
#   ./test-all.sh --quick         static checks + unit tests only (no network)
#   ./test-all.sh --full          everything, plus heavy dependency graphs and the real-project corpus
#   ./test-all.sh --corpus-only   only the real-project corpus
#
# Options: --only PATTERN   run only scenarios whose name contains PATTERN
#          --run-tests      in the corpus, also compare `jv test` vs `mvn test` exit status
#          --corpus-file F  use F instead of the built-in corpus (lines: name|git url|ref|subdir|cp or build)
#          --jv PATH        test an existing binary instead of building one
#          --work DIR       use DIR as the scratch directory (default: a fresh temp dir)
#          --keep           keep the scratch directory (it is always kept when something fails)
#          --skip-go        skip gofmt/vet/go test
#
# Environment: STRICT_ORDER=1 makes classpath *order* differences against Maven a failure
#              (otherwise they are reported as warnings; the set of jars must always match).
#
# Needs: go (unless --jv), java + javac 17+, bash. Optional: mvn (parity checks), gradle (real-Gradle run), git (corpus),
# curl, python3.

set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

TIER=default; ONLY=""; RUN_TESTS=0; CORPUS_FILE=""; JVBIN=""; WORK=""; KEEP=0; SKIP_GO=0
while [ $# -gt 0 ]; do
  case "$1" in
    --quick) TIER=quick ;;
    --full) TIER=full ;;
    --corpus-only) TIER=corpus ;;
    --only) ONLY="${2:-}"; shift ;;
    --run-tests) RUN_TESTS=1 ;;
    --corpus-file) CORPUS_FILE="${2:-}"; shift ;;
    --jv) JVBIN="${2:-}"; shift ;;
    --work) WORK="${2:-}"; shift ;;
    --keep) KEEP=1 ;;
    --skip-go) SKIP_GO=1 ;;
    -h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1 (try --help)"; exit 2 ;;
  esac
  shift
done

# ---------------------------------------------------------------- output helpers
if [ -t 1 ]; then RED=$'\033[31m'; GRN=$'\033[32m'; YEL=$'\033[33m'; BLD=$'\033[1m'; RST=$'\033[0m'; else RED=; GRN=; YEL=; BLD=; RST=; fi
PASS=0; FAIL=0; SKIP=0; WARN=0; FAILED=""; TIMES=""
ok()   { PASS=$((PASS+1)); printf '  %sPASS%s %s\n' "$GRN" "$RST" "$1"; }
bad()  { FAIL=$((FAIL+1)); FAILED="$FAILED"$'\n'"  - $1"; printf '  %sFAIL%s %s\n' "$RED" "$RST" "$1"; [ -n "${2:-}" ] && printf '       %s\n' "$2"; }
skip() { SKIP=$((SKIP+1)); printf '  %sSKIP%s %s (%s)\n' "$YEL" "$RST" "$1" "${2:-}"; }
warn() { WARN=$((WARN+1)); printf '  %sWARN%s %s\n' "$YEL" "$RST" "$1"; }
section() { printf '\n%s== %s ==%s\n' "$BLD" "$1" "$RST"; }
want() { [ -z "$ONLY" ] || case "$1" in *"$ONLY"*) return 0 ;; *) return 1 ;; esac; }

now() {
  if [ -n "${EPOCHREALTIME:-}" ]; then echo "${EPOCHREALTIME/,/.}"
  elif command -v python3 >/dev/null 2>&1; then python3 -c 'import time;print(time.time())'
  else date +%s; fi
}
elapsed() { awk -v a="$1" -v b="$2" 'BEGIN{printf "%.1f", b-a}'; }
mtime() { stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null; }

# ---------------------------------------------------------------- environment
[ -n "$WORK" ] || WORK="$(mktemp -d "${TMPDIR:-/tmp}/jv-test.XXXXXX")"
mkdir -p "$WORK/logs"; LOGDIR="$WORK/logs"
cleanup() { if [ "$KEEP" = 0 ] && [ "$FAIL" = 0 ]; then rm -rf "$WORK"; fi; }
trap cleanup EXIT

HAVE_MVN=0; command -v mvn >/dev/null 2>&1 && HAVE_MVN=1
HAVE_JAVAC=0; command -v javac >/dev/null 2>&1 && HAVE_JAVAC=1
HAVE_GIT=0; command -v git >/dev/null 2>&1 && HAVE_GIT=1

section "Environment"
echo "  scratch dir : $WORK"
[ "$HAVE_JAVAC" = 1 ] && echo "  javac       : $(javac -version 2>&1)" || echo "  javac       : NOT FOUND (compile/test scenarios will be skipped)"
[ "$HAVE_MVN" = 1 ] && echo "  maven       : $(mvn -v 2>/dev/null | head -1)  -> parity checks ENABLED" || echo "  maven       : not installed -> parity checks DISABLED (install Maven for the strongest checks)"

# ---------------------------------------------------------------- build / static checks
build_jv() {
  if [ -n "$JVBIN" ]; then return 0; fi
  JVBIN="$WORK/jv"
  (cd "$ROOT" && go build -trimpath -o "$JVBIN" .) >"$LOGDIR/build.log" 2>&1
}

if [ "$TIER" != corpus ] && [ "$SKIP_GO" = 0 ] && [ -z "$JVBIN" ]; then
  section "Static checks and unit tests"
  if ! command -v go >/dev/null 2>&1; then
    bad "go toolchain" "go not found; install Go or pass --jv PATH --skip-go"
  else
    fmt=$(cd "$ROOT" && gofmt -l . 2>&1)
    [ -z "$fmt" ] && ok "gofmt" || bad "gofmt" "unformatted: $fmt"
    (cd "$ROOT" && go vet ./...) >"$LOGDIR/vet.log" 2>&1 && ok "go vet" || bad "go vet" "see $LOGDIR/vet.log"
    (cd "$ROOT" && go test -race -count=1 ./...) >"$LOGDIR/gotest.log" 2>&1
    rc=$?
    if [ $rc -ne 0 ] && grep -qi 'cgo' "$LOGDIR/gotest.log"; then
      (cd "$ROOT" && go test -count=1 ./...) >"$LOGDIR/gotest.log" 2>&1; rc=$?
      [ $rc -eq 0 ] && warn "race detector unavailable (no cgo); ran tests without -race"
    fi
    [ $rc -eq 0 ] && ok "go test (unit + in-process reactor build)" || bad "go test" "see $LOGDIR/gotest.log"
    for target in linux/arm64 darwin/arm64 darwin/amd64 windows/amd64; do
      (cd "$ROOT" && GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 go build -o /dev/null .) >"$LOGDIR/cross.log" 2>&1 \
        && ok "cross-compile $target" || bad "cross-compile $target" "see $LOGDIR/cross.log"
    done
  fi
fi

if [ "$TIER" = quick ]; then SKIP_E2E=1; else SKIP_E2E=0; fi
if [ "$SKIP_E2E" = 0 ]; then
  if [ -z "$JVBIN" ] && ! command -v go >/dev/null 2>&1; then bad "build jv" "go not found and no --jv given"; SKIP_E2E=1
  elif ! build_jv; then bad "build jv" "see $LOGDIR/build.log"; SKIP_E2E=1
  elif [ ! -x "$JVBIN" ]; then bad "jv binary" "$JVBIN is not executable"; SKIP_E2E=1; fi
fi

# ---------------------------------------------------------------- test helpers
LAST_OUT=""; LAST_RC=0; CUR=""; LOG=""
begin() { CUR="$1"; LOG="$LOGDIR/$1.log"; : >"$LOG"; section "Scenario: $1"; }
runjv() { # dir args...   (classpath calls are forced quiet so LAST_OUT is a clean path list)
  local dir=$1; shift
  case " $* " in *" classpath "*) set -- -q "$@" ;; esac
  LAST_OUT=$(cd "$dir" && "$JVBIN" "$@" 2>&1); LAST_RC=$?
  printf '$ (cd %s && jv %s)\n%s\n(rc=%s)\n\n' "$dir" "$*" "$LAST_OUT" "$LAST_RC" >>"$LOG"
}
t_rc0()  { [ "$LAST_RC" = 0 ] && ok "$CUR: $1" || bad "$CUR: $1" "exit code $LAST_RC; see $LOG"; }
t_rcnz() { [ "$LAST_RC" != 0 ] && ok "$CUR: $1" || bad "$CUR: $1" "expected a non-zero exit code; see $LOG"; }
t_rc()   { [ "$LAST_RC" = "$2" ] && ok "$CUR: $1" || bad "$CUR: $1" "expected exit $2, got $LAST_RC; see $LOG"; }
t_out()  { printf '%s' "$LAST_OUT" | grep -Eq -- "$2" && ok "$CUR: $1" || bad "$CUR: $1" "output lacks /$2/; see $LOG"; }
t_noout(){ printf '%s' "$LAST_OUT" | grep -Eq -- "$2" && bad "$CUR: $1" "output unexpectedly matches /$2/; see $LOG" || ok "$CUR: $1"; }
t_file() { [ -e "$2" ] && ok "$CUR: $1" || bad "$CUR: $1" "missing $2"; }
t_nofile(){ [ ! -e "$2" ] && ok "$CUR: $1" || bad "$CUR: $1" "unexpectedly exists: $2"; }
t_nopanic() { printf '%s' "$LAST_OUT" | grep -Eq 'panic:|goroutine [0-9]+ \[' && bad "$CUR: no Go panic" "see $LOG" || ok "$CUR: no Go panic"; }

cp_list() { printf '%s' "$1" | tr ':' '\n' | sed '/^$/d'; }
cp_has()  { cp_list "$1" | grep -Eq -- "$2"; }
t_cp_has() { cp_has "$2" "$3" && ok "$CUR: $1" || bad "$CUR: $1" "classpath lacks /$3/"; }
t_cp_not() { cp_has "$2" "$3" && bad "$CUR: $1" "classpath unexpectedly has /$3/" || ok "$CUR: $1"; }

valid_jars() { # classpath string -> checks every jar exists and is a valid zip
  local cp=$1
  if command -v python3 >/dev/null 2>&1; then
    cp_list "$cp" | python3 -c '
import sys, zipfile, os
bad = []
for p in (l.strip() for l in sys.stdin):
    if os.path.isdir(p): continue
    try:
        assert os.path.isfile(p), "missing"
        assert zipfile.ZipFile(p).testzip() is None, "corrupt entry"
    except Exception as e:
        bad.append(p + ": " + str(e))
print("\n".join(bad)); sys.exit(1 if bad else 0)'
  else
    local p; while IFS= read -r p; do [ -d "$p" ] || jar tf "$p" >/dev/null 2>&1 || { echo "$p: invalid"; return 1; }; done < <(cp_list "$cp")
  fi
}

dep() { # g a v [scope] [extra-xml]
  printf '<dependency><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version>%s%s</dependency>' \
    "$1" "$2" "$3" "${4:+<scope>$4</scope>}" "${5:-}"
}

newproj() { # dir "xml placed inside <project>" [artifactId]
  local dir=$1 body=$2 art=${3:-app}
  mkdir -p "$dir/src/main/java/demo" "$dir/src/test/java/demo"
  cat >"$dir/pom.xml" <<EOF
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId><artifactId>$art</artifactId><version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
    <jv.mainClass>demo.App</jv.mainClass>
  </properties>
  $body
</project>
EOF
  cat >"$dir/src/main/java/demo/App.java" <<'EOF'
package demo;
public class App {
    public static String greet(String n) { return "Hello, " + n; }
    public static void main(String[] args) { System.out.println(greet("jv")); }
}
EOF
}

junit_test() { # dir  (a passing JUnit 5 test)
  cat >"$1/src/test/java/demo/AppTest.java" <<'EOF'
package demo;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;
class AppTest { @Test void greets() { assertEquals("Hello, x", App.greet("x")); } }
EOF
}

NET_OK=1
check_network() {
  [ "${NO_NET_CHECK:-0}" = 1 ] && return 0
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -m 15 -o /dev/null https://repo1.maven.org/maven2/junit/junit/maven-metadata.xml 2>/dev/null || NET_OK=0
  fi
}

# Compare jv's classpath with Maven's, for all three scopes. $3.. are flags given to BOTH tools.
# Set MVN_EXTRA (array-free: a string of extra mvn args) and JV_REPO_ENV for custom repos.

# A mistyped plugin property once made mvn return the *test* classpath for every scope and
# looked like a jv bug. Verify the oracle really honours includeScope before trusting it.
MVN_CANARY_DONE=0
mvn_canary() {
  [ "$HAVE_MVN" = 1 ] && [ "$MVN_CANARY_DONE" = 0 ] || return 0
  MVN_CANARY_DONE=1
  local d="$WORK/canary" out="$WORK/canary.cp"
  mkdir -p "$d"
  cat >"$d/pom.xml" <<EOF
<project><modelVersion>4.0.0</modelVersion><groupId>demo</groupId><artifactId>canary</artifactId><version>1</version>
<dependencies>$(dep commons-codec commons-codec 1.16.0)$(dep commons-io commons-io 2.15.1 test)</dependencies></project>
EOF
  rm -f "$out"
  mvn -q -B -ntp -f "$d/pom.xml" dependency:build-classpath -Dmdep.outputFile="$out" -DincludeScope=compile >"$LOGDIR/canary.log" 2>&1
  if [ -f "$out" ] && grep -q commons-codec "$out" && ! grep -q commons-io "$out"; then
    ok "harness canary: mvn honours includeScope"
  else
    bad "harness canary: mvn ignored includeScope, parity checks disabled" "see $LOGDIR/canary.log"
    HAVE_MVN=0
  fi
}

MVN_EXTRA=""
parity() { # label dir [flags...]
  local label=$1 dir=$2; shift 2
  mvn_canary
  if [ "$HAVE_MVN" = 0 ]; then skip "$label: classpath parity vs mvn" "mvn not installed or its oracle is unreliable"; return; fi
  local scope out_m out_j rc
  for scope in compile runtime test; do
    out_m="$WORK/parity-$$.mvn"; out_j="$WORK/parity-$$.jv"; rm -f "$out_m" "$out_j"
    # shellcheck disable=SC2086
    mvn -q -B -ntp -f "$dir/pom.xml" "$@" $MVN_EXTRA dependency:build-classpath -Dmdep.outputFile="$out_m" -DincludeScope="$scope" \
      >"$LOGDIR/parity-mvn.log" 2>&1
    if [ $? -ne 0 ] || [ ! -f "$out_m" ]; then skip "$label: classpath[$scope] parity" "mvn could not resolve this project (see $LOGDIR/parity-mvn.log)"; continue; fi
    (cd "$dir" && "$JVBIN" -q "$@" classpath "$scope") >"$out_j" 2>"$LOGDIR/parity-jv.log"; rc=$?
    if [ $rc -ne 0 ]; then bad "$label: classpath[$scope] parity" "jv failed (exit $rc) where mvn succeeded: $(tail -3 "$LOGDIR/parity-jv.log" | tr '\n' ' ')"; continue; fi
    cp_list "$(cat "$out_m")" | sort >"$out_m.sorted"; cp_list "$(cat "$out_j")" | sort >"$out_j.sorted"
    if diff "$out_m.sorted" "$out_j.sorted" >"$LOGDIR/parity-diff.log" 2>&1; then
      if cmp -s <(cp_list "$(cat "$out_m")") <(cp_list "$(cat "$out_j")"); then ok "$label: classpath[$scope] identical to mvn ($(wc -l <"$out_m.sorted" | tr -d ' ') entries)"
      elif [ "${STRICT_ORDER:-0}" = 1 ]; then bad "$label: classpath[$scope] order differs from mvn"
      else ok "$label: classpath[$scope] same jars as mvn"; warn "$label: classpath[$scope] ORDER differs from mvn (set STRICT_ORDER=1 to fail)"; fi
    else
      cp "$LOGDIR/parity-diff.log" "$LOGDIR/parity-diff-$(echo "$label" | tr -c 'A-Za-z0-9\n' _)-$scope.log"
      bad "$label: classpath[$scope] differs from mvn" "< mvn only / > jv only; see $LOGDIR/parity-diff-$(echo "$label" | tr -c 'A-Za-z0-9\n' _)-$scope.log"
      head -6 "$LOGDIR/parity-diff.log" | sed 's/^/         /'
    fi
  done
}

# ---------------------------------------------------------------- scenarios
sc_basic() {
  begin basic
  local d="$WORK/basic"
  newproj "$d" "<dependencies></dependencies>"
  runjv "$d" -q compile;                   t_rc0 "quiet compile succeeds"; [ -z "$LAST_OUT" ] && ok "$CUR: -q prints nothing on success" || bad "$CUR: -q was not quiet" "$LAST_OUT"
  runjv "$d" classpath;                    t_rc0 "classpath of a dependency-free project"; [ -z "$LAST_OUT" ] && ok "$CUR: classpath is empty" || bad "$CUR: classpath not empty" "$LAST_OUT"
  runjv "$d" tree;                         t_rc0 "tree"; t_out "tree shows the project" 'demo:app:1\.0'
  runjv "$d" -q package;                   t_rc0 "package"; t_file "jar" "$d/target/app-1.0.jar"
  [ "$(java -jar "$d/target/app-1.0.jar" 2>&1)" = "Hello, jv" ] && ok "$CUR: java -jar runs (Main-Class from jv.mainClass)" || bad "$CUR: java -jar failed"
  runjv "$d" -q package;                   t_rc0 "repackage"
  local h1 h2; h1=$(cksum <"$d/target/app-1.0.jar"); runjv "$d" -q clean; runjv "$d" -q package; h2=$(cksum <"$d/target/app-1.0.jar")
  [ "$h1" = "$h2" ] && ok "$CUR: jar is reproducible across clean builds" || bad "$CUR: jar differs between identical builds"
  mkdir -p "$d/src/main/resources"; echo hello >"$d/src/main/resources/r.txt"
  runjv "$d" -q compile;                   t_file "resources copied" "$d/target/classes/r.txt"
  runjv "$d" -DskipTests -q install;       t_rc0 "install"
}

sc_single() {
  begin single
  local d="$WORK/single"
  newproj "$d" "<dependencies>$(dep com.google.guava guava 31.1-jre)$(dep org.junit.jupiter junit-jupiter-api 5.10.2 test)</dependencies>"
  junit_test "$d"
  runjv "$d" compile;                      t_rc0 "compile";        t_file "App.class produced" "$d/target/classes/demo/App.class"
  local m1; m1=$(mtime "$d/target/classes/demo/App.class")
  runjv "$d" compile;                      t_rc0 "second compile"; t_out "reuses cached resolution" 'cached resolution'; t_out "skips javac" 'up to date'
  [ "$(mtime "$d/target/classes/demo/App.class")" = "$m1" ] && ok "$CUR: class file untouched when nothing changed" || bad "$CUR: unchanged project was recompiled"
  runjv "$d" test;                         t_rc0 "test";           t_out "JUnit ran the test" '1 tests successful'
  runjv "$d" -DskipTests package;          t_rc0 "package";        t_file "jar built" "$d/target/app-1.0.jar"
  jar tf "$d/target/app-1.0.jar" 2>/dev/null | grep -q 'demo/App.class' && ok "$CUR: jar contains App.class" || bad "$CUR: jar lacks App.class"
  runjv "$d" classpath runtime
  local rcp=$LAST_OUT
  [ "$(java -cp "$d/target/app-1.0.jar:$rcp" demo.App 2>&1)" = "Hello, jv" ] && ok "$CUR: packaged app runs" || bad "$CUR: packaged app did not run"
  sleep 1; echo "// touched" >>"$d/src/main/java/demo/App.java"
  runjv "$d" compile;                      t_out "source edit triggers recompile" 'compiled main sources'
  sleep 1
  sed -i.bak "s#</dependencies>#$(dep org.apache.commons commons-lang3 3.14.0)</dependencies>#" "$d/pom.xml" && rm -f "$d/pom.xml.bak"
  runjv "$d" compile;                      t_out "pom edit re-resolves" 'resolving dependencies'
  runjv "$d" classpath compile;            t_cp_has "new dependency on classpath" "$LAST_OUT" 'commons-lang3-3\.14\.0\.jar'
  runjv "$d" clean;                        t_nofile "clean removes target/" "$d/target"; t_nofile "clean removes .jv/" "$d/.jv"
  parity single "$d"
}

sc_failing_test() {
  begin failing_test
  local d="$WORK/failtest"
  newproj "$d" "<dependencies>$(dep org.junit.jupiter junit-jupiter-api 5.10.2 test)</dependencies>"
  cat >"$d/src/test/java/demo/BadTest.java" <<'EOF'
package demo;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;
class BadTest { @Test void fails() { assertEquals(1, 2); } }
EOF
  runjv "$d" test;                         t_rcnz "failing test fails the build"; t_out "failure is reported" '1 tests failed'
  runjv "$d" -DskipTests package;          t_rc0 "-DskipTests still packages"
  runjv "$d" -Dmaven.test.skip=true package; t_rc0 "-Dmaven.test.skip=true packages"
}

sc_compile_error() {
  begin compile_error
  local d="$WORK/cerr"
  newproj "$d" "<dependencies></dependencies>"
  runjv "$d" compile;                      t_rc0 "baseline compiles"
  sleep 1; echo 'class Broken { int x = ; }' >"$d/src/main/java/demo/Broken.java"
  runjv "$d" compile;                      t_rcnz "syntax error fails"
  runjv "$d" compile;                      t_rcnz "still fails on retry (no stale 'up to date' stamp)"
  rm "$d/src/main/java/demo/Broken.java"
  runjv "$d" compile;                      t_rc0 "recovers after the fix"
  t_nofile "no leftover Broken.class" "$d/target/classes/Broken.class"
}

sc_scopes() {
  begin scopes
  local d="$WORK/scopes"
  newproj "$d" "<dependencies>
    $(dep com.google.guava guava 31.1-jre '' '<exclusions><exclusion><groupId>com.google.guava</groupId><artifactId>failureaccess</artifactId></exclusion></exclusions>')
    $(dep jakarta.servlet jakarta.servlet-api 6.0.0 provided)
    $(dep org.apache.commons commons-lang3 3.14.0 runtime)
    $(dep org.junit.jupiter junit-jupiter-api 5.10.2 test)
  </dependencies>"
  runjv "$d" classpath compile; local c=$LAST_OUT
  t_cp_has "compile has compile+provided" "$c" 'guava-31\.1-jre'; t_cp_has "compile has provided" "$c" 'jakarta.servlet-api'
  t_cp_not "compile lacks runtime" "$c" 'commons-lang3'; t_cp_not "compile lacks test" "$c" 'junit-jupiter-api'
  runjv "$d" classpath runtime; local r=$LAST_OUT
  t_cp_has "runtime has runtime" "$r" 'commons-lang3'; t_cp_not "runtime lacks provided" "$r" 'jakarta.servlet-api'; t_cp_not "runtime lacks test" "$r" 'junit-jupiter-api'
  runjv "$d" classpath test; local t=$LAST_OUT
  t_cp_has "test has everything (junit)" "$t" 'junit-jupiter-api'; t_cp_has "test has provided" "$t" 'jakarta.servlet-api'
  t_cp_not "exclusion honoured" "$t" 'failureaccess'
  runjv "$d" tree; t_rc0 "tree"; t_out "tree lists guava" 'guava:31\.1-jre'
  parity scopes "$d"
}

sc_ranges() {
  begin ranges
  local d="$WORK/ranges"
  newproj "$d" "<dependencies>$(dep com.google.guava guava '[31.0-jre,32.0-jre)')</dependencies>"
  runjv "$d" classpath compile;            t_rc0 "range resolves"; t_cp_has "highest version inside the range (Maven orders 32.0.0-android below 32.0-jre)" "$LAST_OUT" 'guava-32\.0\.0-android\.jar'
  parity ranges "$d"
}

sc_profiles() {
  begin profiles
  local d="$WORK/profiles"
  newproj "$d" "<dependencies></dependencies>
  <profiles>
    <profile><id>base</id><activation><activeByDefault>true</activeByDefault></activation><dependencies>$(dep commons-codec commons-codec 1.16.0)</dependencies></profile>
    <profile><id>extra</id><dependencies>$(dep org.apache.commons commons-lang3 3.14.0)</dependencies></profile>
    <profile><id>byprop</id><activation><property><name>with-io</name></property></activation><dependencies>$(dep commons-io commons-io 2.15.1)</dependencies></profile>
  </profiles>"
  runjv "$d" classpath compile
  t_cp_has "activeByDefault applies" "$LAST_OUT" 'commons-codec'; t_cp_not "other profiles off" "$LAST_OUT" 'commons-(lang3|io)'
  runjv "$d" -Pextra classpath compile
  t_cp_has "-Pextra applies" "$LAST_OUT" 'commons-lang3'; t_cp_not "-Pextra deactivates activeByDefault" "$LAST_OUT" 'commons-codec'
  runjv "$d" -Dwith-io=1 classpath compile
  t_cp_has "property activation applies" "$LAST_OUT" 'commons-io'; t_cp_not "property activation deactivates default" "$LAST_OUT" 'commons-codec'
  runjv "$d" '-P!base' -Pextra classpath compile
  t_cp_not "-P!id deactivates" "$LAST_OUT" 'commons-codec'
  parity "profiles(default)" "$d"
  parity "profiles(-Pextra)" "$d" -Pextra
  parity "profiles(-Dwith-io)" "$d" -Dwith-io=1
}

write_reactor() { # dir
  local d=$1
  mkdir -p "$d"/{core,util,app}/src/main/java/demo "$d"/{core,app}/src/test/java/demo
  cat >"$d/pom.xml" <<EOF
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId><artifactId>root</artifactId><version>1.0</version><packaging>pom</packaging>
  <properties><maven.compiler.release>17</maven.compiler.release><project.build.sourceEncoding>UTF-8</project.build.sourceEncoding></properties>
  <modules><module>app</module><module>util</module><module>core</module></modules>
  <dependencyManagement><dependencies>
    $(dep com.google.guava guava 31.1-jre)
    $(dep org.junit junit-bom 5.10.2 import | sed 's#</version>#</version><type>pom</type>#')
  </dependencies></dependencyManagement>
</project>
EOF
  local hdr='<project><modelVersion>4.0.0</modelVersion><parent><groupId>demo</groupId><artifactId>root</artifactId><version>1.0</version></parent>'
  cat >"$d/core/pom.xml" <<EOF
$hdr<artifactId>core</artifactId><dependencies>
<dependency><groupId>com.google.guava</groupId><artifactId>guava</artifactId></dependency>
<dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter-api</artifactId><scope>test</scope></dependency>
</dependencies></project>
EOF
  cat >"$d/util/pom.xml" <<EOF
$hdr<artifactId>util</artifactId><dependencies>
<dependency><groupId>demo</groupId><artifactId>core</artifactId><version>\${project.version}</version></dependency>
</dependencies></project>
EOF
  cat >"$d/app/pom.xml" <<EOF
$hdr<artifactId>app</artifactId><properties><jv.mainClass>demo.App</jv.mainClass></properties><dependencies>
<dependency><groupId>demo</groupId><artifactId>util</artifactId><version>\${project.version}</version></dependency>
<dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter-api</artifactId><scope>test</scope></dependency>
</dependencies></project>
EOF
  cat >"$d/core/src/main/java/demo/Core.java" <<'EOF'
package demo;
import com.google.common.base.Strings;
public class Core { public static String pad(String s) { return Strings.padStart(s, 6, '.'); } }
EOF
  cat >"$d/util/src/main/java/demo/Util.java" <<'EOF'
package demo;
public class Util { public static String shout(String s) { return Core.pad(s.toUpperCase()); } }
EOF
  cat >"$d/app/src/main/java/demo/App.java" <<'EOF'
package demo;
public class App { public static void main(String[] a) { System.out.println(Util.shout("hi")); } }
EOF
  cat >"$d/core/src/test/java/demo/CoreTest.java" <<'EOF'
package demo;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;
class CoreTest { @Test void pads() { assertEquals("....ab", Core.pad("ab")); } }
EOF
  cat >"$d/app/src/test/java/demo/AppTest.java" <<'EOF'
package demo;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;
class AppTest { @Test void shouts() { assertEquals("....HI", Util.shout("hi")); } }
EOF
}

sc_reactor() {
  begin reactor
  local d="$WORK/reactor" repo="$WORK/repo-reactor"
  write_reactor "$d"
  runjv "$d" -DskipTests package;          t_rc0 "reactor builds (modules declared out of order)"
  local lc lu la
  lc=$(printf '%s\n' "$LAST_OUT" | grep -n '\[core\]' | head -1 | cut -d: -f1)
  lu=$(printf '%s\n' "$LAST_OUT" | grep -n '\[util\]' | head -1 | cut -d: -f1)
  la=$(printf '%s\n' "$LAST_OUT" | grep -n '\[app\]' | head -1 | cut -d: -f1)
  if [ -n "$lc" ] && [ -n "$lu" ] && [ -n "$la" ] && [ "$lc" -lt "$lu" ] && [ "$lu" -lt "$la" ]; then ok "$CUR: built in dependency order core < util < app"; else bad "$CUR: build order wrong (core=$lc util=$lu app=$la)"; fi
  t_file "app jar" "$d/app/target/app-1.0.jar"
  runjv "$d" -pl app classpath runtime
  [ "$(java -cp "$d/app/target/app-1.0.jar:$LAST_OUT" demo.App 2>&1)" = "....HI" ] \
    && ok "$CUR: modules work together at runtime" || bad "$CUR: built modules do not run together"
  runjv "$d" test;                         t_rc0 "tests in several modules pass"
  runjv "$d" -q -pl util compile;          t_rc0 "-pl util"
  runjv "$d" -pl util compile;             t_out "-pl pulls in upstream core" '\[core\]'; t_noout "-pl leaves unrelated modules alone" '\[app\]'
  runjv "$d" -pl nope compile;             t_rcnz "-pl with unknown module fails"
  runjv "$d" classpath;                    t_rcnz "classpath at the aggregator needs -pl"
  runjv "$d" -q -pl app classpath test;    t_rc0 "-pl app classpath"; t_cp_has "sibling build output on classpath" "$LAST_OUT" 'util/target/classes'
  sleep 1; echo "// edit" >>"$d/core/src/main/java/demo/Core.java"
  runjv "$d" compile
  t_out "edit in core rebuilds core" '\[core\] compiled main sources'; t_out "...and dependent util" '\[util\] compiled main sources'; t_out "...and dependent app" '\[app\] compiled main sources'
  runjv "$d" compile;                      t_noout "second run recompiles nothing" 'compiled main sources'
  export JV_REPO="$repo"; rm -rf "$repo"
  runjv "$d" -DskipTests install;          t_rc0 "install"
  t_file "core jar installed" "$repo/demo/core/1.0/core-1.0.jar"; t_file "root pom installed" "$repo/demo/root/1.0/root-1.0.pom"
  # a consumer that only knows the installed artifacts (parent + BOM import must resolve from the installed POMs)
  local c="$WORK/consumer"; newproj "$c" "<dependencies>$(dep demo app 1.0)</dependencies>" consumer
  sed -i.bak '3s#<artifactId>app</artifactId>#<artifactId>consumer</artifactId>#' "$c/pom.xml" && rm -f "$c/pom.xml.bak" # not demo:app: that would depend on itself
  runjv "$c" classpath compile;            t_rc0 "consumer resolves installed artifacts"
  t_cp_has "consumer sees app" "$LAST_OUT" 'app-1\.0\.jar'; t_cp_has "...transitive util" "$LAST_OUT" 'util-1\.0\.jar'
  t_cp_has "...transitive core" "$LAST_OUT" 'core-1\.0\.jar'; t_cp_has "...guava via core (managed by installed parent)" "$LAST_OUT" 'guava-31\.1-jre'
  MVN_EXTRA="-Dmaven.repo.local=$repo" parity "reactor-consumer" "$c"
  unset JV_REPO
  # Maven parity inside the reactor: mvn needs 'compile' in the same invocation to resolve siblings
  if [ "$HAVE_MVN" = 1 ]; then
    local mo="$WORK/reactor.mvn"
    mvn -q -B -ntp -f "$d/pom.xml" compile dependency:build-classpath -pl app -am -Dmdep.outputFile="$mo" -DincludeScope=test >"$LOGDIR/parity-mvn.log" 2>&1
    if [ -f "$mo" ]; then
      (cd "$d" && "$JVBIN" -q -pl app classpath test) >"$WORK/reactor.jv" 2>/dev/null
      if diff <(cp_list "$(cat "$mo")" | sort) <(cp_list "$(cat "$WORK/reactor.jv")" | sort) >"$LOGDIR/parity-diff.log" 2>&1; then ok "$CUR: reactor classpath identical to mvn -pl app -am"
      else bad "$CUR: reactor classpath differs from mvn" "see $LOGDIR/parity-diff.log"; fi
    else skip "$CUR: reactor parity" "mvn reactor build failed (see $LOGDIR/parity-mvn.log)"; fi
  fi
}

sc_offline() {
  begin offline
  local d="$WORK/offl" repo="$WORK/repo-offline"
  newproj "$d" "<dependencies>$(dep com.google.guava guava 31.1-jre)</dependencies>"
  export JV_REPO="$repo"; rm -rf "$repo"
  runjv "$d" -o compile;                   t_rcnz "-o on an empty repo fails"; t_out "says why" '[Oo]ffline'; t_nopanic
  runjv "$d" compile;                      t_rc0 "online run populates the repo"
  runjv "$d" clean
  runjv "$d" -o compile;                   t_rc0 "-o succeeds from the populated repo"
  unset JV_REPO
}

sc_errors() {
  begin errors
  local d="$WORK/errs"
  newproj "$d" "<dependencies>$(dep demo nope 1.0-SNAPSHOT)</dependencies>"
  runjv "$d" compile;                      t_rcnz "unpublished SNAPSHOT fails"; t_out "message names SNAPSHOT" 'SNAPSHOT'; t_nopanic
  newproj "$d" "<dependencies>$(dep demo does-not-exist 9.9.9)</dependencies>"
  runjv "$d" compile;                      t_rcnz "missing artifact fails"; t_out "message names the artifact" 'does-not-exist'; t_nopanic
  newproj "$d" "<dependencies>$(dep com.google.guava guava '[99.0,100.0)')</dependencies>"
  runjv "$d" compile;                      t_rcnz "unsatisfiable range fails"; t_nopanic
  newproj "$d" "<dependencies><dependency><groupId>g</groupId><artifactId>noversion</artifactId></dependency></dependencies>"
  runjv "$d" compile;                      t_rcnz "dependency without a version fails"; t_out "explains" 'no version'; t_nopanic
  newproj "$d" "<dependencies>$(dep demo app 1.0)</dependencies>"
  runjv "$d" compile;                      t_rcnz "a project depending on itself fails"; t_out "says so" 'depends on itself'; t_nopanic
  printf '<project><unclosed>' >"$d/pom.xml"
  runjv "$d" compile;                      t_rcnz "malformed pom.xml fails cleanly"; t_nopanic
  runjv "$d" --bogus compile;              t_out "unknown flag is delegated, not rejected" 'not handled natively'; t_nopanic
  runjv "$d" --version;                    t_rc0 "--version"; t_out "prints version" '^jv '
  runjv "$d" --help;                       t_rc0 "--help"; t_out "shows usage" 'Usage: jv'
  runjv "$d";                              t_rc "no goal exits 2" 2
}

sc_fallback() {
  begin fallback
  local d="$WORK/fb"
  newproj "$d" "<dependencies></dependencies>"
  if [ "$HAVE_MVN" = 1 ]; then
    runjv "$d" -q validate;                t_rc0 "unknown goal is delegated to mvn"
    sed -i.bak 's#<groupId>demo</groupId>#<groupId>demo</groupId><packaging>war</packaging>#' "$d/pom.xml" && rm -f "$d/pom.xml.bak"
    runjv "$d" compile;                    t_out "war packaging is delegated" 'delegating to mvn'
  else
    skip "mvn delegation" "mvn not installed"
  fi
}

# ---------------------------------------------------------------- Gradle
# A Gradle project is built natively from a model a real Gradle reports once (see README). These
# scenarios stand in for Gradle with a stub `gradle` that answers the model query from a canned
# JSON file, so they need no Gradle at all: jv compiles, packages and cleans with javac.
stub_queries() { local n; n=$(grep -c 'jvModel' "$JV_STUB_LOG" 2>/dev/null); echo "${n:-0}"; }

gradle_proj() { # dir path name [classpath-json] [mainRes-json]   one Java project as the model reports it
  local dir=$1 path=$2 name=$3 cp=${4:-[]} res=${5:-[]}
  cat <<EOF
{"path":"$path","dir":"$dir","name":"$name","group":"demo","version":"1.0","buildDir":"$dir/build",
"plugins":["org.gradle.api.plugins.JavaPlugin"],"java":true,
"mainJava":["$dir/src/main/java"],"mainRes":$res,"testJava":[],"testRes":[],
"mainClasses":["$dir/build/classes/java/main"],"testClasses":["$dir/build/classes/java/test"],
"mainResOut":"$dir/build/resources/main","testResOut":"$dir/build/resources/test",
"compile":$cp,"runtime":$cp,"testCompile":$cp,"testRuntime":$cp,"processors":[],"compilerArgs":[],
"encoding":"UTF-8","release":"","source":"","target":"","compileDeps":["compileJava"],
"testEnabled":true,"testFramework":"","jvmArgs":[],"systemProps":{},
"jar":"$dir/build/libs/$name-1.0.jar","manifest":{"Main-Class":"demo.App"}}
EOF
}

sc_gradle() {
  begin gradle
  local d="$WORK/gradle-single" m="$WORK/gradle-multi" bindir="$WORK/gradle-stub-bin" oldpath=$PATH
  mkdir -p "$bindir" "$d/src/main/java/demo" "$d/src/main/resources" "$m/core/src/main/java/demo" "$m/app/src/main/java/demo"
  cat >"$bindir/gradle" <<'EOF'
#!/bin/sh
# stub gradle: log the arguments, answer the model query from $JV_STUB_MODEL, otherwise exit $JV_STUB_RC
echo "$*" >>"$JV_STUB_LOG"
for a in "$@"; do
  case "$a" in -Djv.model.out=*) cp "$JV_STUB_MODEL" "${a#-Djv.model.out=}"; exit 0 ;; esac
done
exit "${JV_STUB_RC:-0}"
EOF
  chmod +x "$bindir/gradle"
  export JV_STUB_LOG="$WORK/gradle-stub.log" JV_STUB_MODEL="$WORK/gradle-single.model.json"
  : >"$JV_STUB_LOG"; PATH="$bindir:$PATH"

  # ---- a single-project build
  echo "plugins { id 'java' }" >"$d/build.gradle"
  cat >"$d/src/main/java/demo/App.java" <<'EOF'
package demo;
public class App {
    public static void main(String[] args) { System.out.println("Hello, jv"); }
}
EOF
  echo hello >"$d/src/main/resources/r.txt"
  printf '{"root":"%s","projects":[%s]}' "$d" "$(gradle_proj "$d" : app '[]' "[\"$d/src/main/resources\"]")" >"$JV_STUB_MODEL"

  runjv "$d" compile;                      t_rc0 "compile from the model"
  t_out "asks Gradle for the model" 'asking Gradle for the project model'
  [ "$(stub_queries)" = 1 ] && ok "$CUR: gradle queried once" || bad "$CUR: gradle queried $(stub_queries) times, expected 1" "see $JV_STUB_LOG"
  t_file "App.class built natively" "$d/build/classes/java/main/demo/App.class"
  t_file "resources copied" "$d/build/resources/main/r.txt"
  runjv "$d" compile;                      t_rc0 "second compile"; t_out "main classes up to date" 'up to date'
  [ "$(stub_queries)" = 1 ] && ok "$CUR: cached model, gradle not started again" || bad "$CUR: gradle started again ($(stub_queries) queries)" "see $JV_STUB_LOG"
  runjv "$d" -DskipTests compile;          t_rc0 "-DskipTests"
  [ "$(stub_queries)" = 1 ] && ok "$CUR: -DskipTests does not change the model" || bad "$CUR: -DskipTests re-queried gradle" "see $JV_STUB_LOG"
  echo "// edited" >>"$d/build.gradle"
  runjv "$d" compile;                      t_rc0 "compile after a build script edit"
  [ "$(stub_queries)" = 2 ] && ok "$CUR: editing build.gradle re-queries gradle" || bad "$CUR: expected 2 queries, got $(stub_queries)" "see $JV_STUB_LOG"
  runjv "$d" -Dfoo=bar compile;            t_rc0 "-D property"
  [ "$(stub_queries)" = 3 ] && grep 'jvModel' "$JV_STUB_LOG" | tail -1 | grep -q -- '-Dfoo=bar' \
    && ok "$CUR: -Dfoo=bar re-queries gradle and is passed to it" || bad "$CUR: -Dfoo=bar not passed to the model query" "see $JV_STUB_LOG"
  runjv "$d" package;                      t_rc0 "package"; t_file "jar built" "$d/build/libs/app-1.0.jar"
  [ "$(java -jar "$d/build/libs/app-1.0.jar" 2>&1)" = "Hello, jv" ] && ok "$CUR: java -jar runs (Main-Class from the model's manifest)" || bad "$CUR: java -jar failed"
  jar tf "$d/build/libs/app-1.0.jar" 2>/dev/null | grep -q 'demo/App.class' && ok "$CUR: jar contains the classes" || bad "$CUR: jar lacks App.class"
  jar tf "$d/build/libs/app-1.0.jar" 2>/dev/null | grep -q 'r.txt' && ok "$CUR: jar contains the resources" || bad "$CUR: jar lacks r.txt"
  runjv "$d" clean;                        t_rc0 "clean"; t_nofile "clean removes build/" "$d/build"; t_file "clean keeps the sources" "$d/src/main/java/demo/App.java"

  # ---- goals Gradle must run itself
  : >"$JV_STUB_LOG"
  runjv "$d" install;                      t_rc0 "install goes to gradle"
  grep -qx 'publishToMavenLocal' "$JV_STUB_LOG" && ok "$CUR: gradle received publishToMavenLocal" || bad "$CUR: install was not translated for gradle" "$(cat "$JV_STUB_LOG")"
  : >"$JV_STUB_LOG"; export JV_GRADLE_ORACLE=0
  runjv "$d" compile;                      t_rc0 "JV_GRADLE_ORACLE=0"
  unset JV_GRADLE_ORACLE
  grep -qx 'classes' "$JV_STUB_LOG" && ok "$CUR: JV_GRADLE_ORACLE=0 runs the bridge (classes task)" || bad "$CUR: bridge not used" "$(cat "$JV_STUB_LOG")"

  # ---- a build jv cannot reproduce (Kotlin) falls back, and Gradle's exit code comes through
  sed 's#org\.gradle\.api\.plugins\.JavaPlugin#org.jetbrains.kotlin.gradle.plugin.KotlinPluginWrapper#' "$JV_STUB_MODEL" >"$WORK/gradle-kotlin.model.json"
  export JV_STUB_MODEL="$WORK/gradle-kotlin.model.json"; : >"$JV_STUB_LOG"
  echo "// kotlin" >>"$d/build.gradle"
  runjv "$d" compile;                      t_rc0 "unsupported build still succeeds"
  t_out "says why it delegates" 'delegating to gradle'
  grep -qx 'classes' "$JV_STUB_LOG" && ok "$CUR: gradle received the translated task" || bad "$CUR: gradle not run for the unsupported build" "$(cat "$JV_STUB_LOG")"
  export JV_STUB_RC=5
  runjv "$d" compile;                      t_rc "gradle's exit code propagates" 5
  unset JV_STUB_RC

  # ---- a multi-project build, declared out of order
  echo "plugins { id 'java' }" >"$m/app/build.gradle"; echo "plugins { id 'java-library' }" >"$m/core/build.gradle"
  printf "rootProject.name = 'multi'\ninclude 'core', 'app'\n" >"$m/settings.gradle"
  cat >"$m/core/src/main/java/demo/Core.java" <<'EOF'
package demo;
public class Core { public static String greet() { return "Hello from core"; } }
EOF
  cat >"$m/app/src/main/java/demo/App.java" <<'EOF'
package demo;
public class App { public static void main(String[] a) { System.out.println(Core.greet()); } }
EOF
  export JV_STUB_MODEL="$WORK/gradle-multi.model.json"; : >"$JV_STUB_LOG"
  printf '{"root":"%s","projects":[{"path":":","dir":"%s","name":"multi","plugins":[],"java":false},%s,%s]}' "$m" "$m" \
    "$(gradle_proj "$m/app" :app app "[\"$m/core/build/libs/core-1.0.jar\"]")" "$(gradle_proj "$m/core" :core core)" >"$JV_STUB_MODEL"
  runjv "$m" compile;                      t_rc0 "multi-project compile"
  t_file "core built" "$m/core/build/classes/java/main/demo/Core.class"; t_file "app built" "$m/app/build/classes/java/main/demo/App.class"
  printf '%s\n' "$LAST_OUT" | awk '/\[core\] compiled/{c=NR} /\[app\] compiled/{a=NR} END{exit !(c && a && c < a)}' \
    && ok "$CUR: core built before app" || bad "$CUR: build order is not core, app" "see $LOG"
  [ "$(java -cp "$m/app/build/classes/java/main:$m/core/build/classes/java/main" demo.App 2>&1)" = "Hello from core" ] && ok "$CUR: modules work together" || bad "$CUR: modules do not work together"
  runjv "$m" compile;                      t_rc0 "second multi-project compile"
  [ "$(stub_queries)" = 1 ] && ok "$CUR: cached although core's jar is not built yet" || bad "$CUR: gradle started again ($(stub_queries) queries): an unbuilt sibling jar invalidated the cache" "see $JV_STUB_LOG"
  rm -rf "$m/core/build" "$m/app/build"
  runjv "$m" -pl core compile;             t_rc0 "-pl core"; t_file "-pl core builds core" "$m/core/build/classes/java/main/demo/Core.class"
  t_nofile "-pl core leaves app alone" "$m/app/build/classes"
  runjv "$m" -pl app compile;              t_rc0 "-pl app"; t_file "-pl app builds app" "$m/app/build/classes/java/main/demo/App.class"
  runjv "$m" -pl app classpath compile;    t_rc0 "classpath of one project"
  t_cp_has "core's classes stand in for its jar" "$LAST_OUT" 'core/build/classes/java/main'; t_cp_not "no unbuilt core jar" "$LAST_OUT" 'core-1\.0\.jar'
  runjv "$m" classpath;                    t_rcnz "classpath at the root is ambiguous"
  runjv "$m" -pl app package;              t_rc0 "-pl app package"; t_file "app jar" "$m/app/build/libs/app-1.0.jar"
  runjv "$m" clean;                        t_rc0 "clean all projects"
  t_nofile "core build/ removed" "$m/core/build"; t_nofile "app build/ removed" "$m/app/build"

  PATH=$oldpath; unset JV_STUB_LOG JV_STUB_MODEL JV_STUB_RC JV_GRADLE_ORACLE
}

# The same flow against a real Gradle, when one is installed and runs here.
sc_gradle_real() {
  begin gradle_real
  if ! command -v gradle >/dev/null 2>&1; then skip "real Gradle" "gradle not installed"; return; fi
  if ! gradle --version >/dev/null 2>&1; then skip "real Gradle" "gradle does not start here (JDK too new for this Gradle?)"; return; fi
  local d="$WORK/gradle-real" ref="$WORK/gradle-real-ref"
  mkdir -p "$d/src/main/java/demo" "$d/src/main/resources"
  printf "plugins { id 'java' }\nversion = '1.0'\njar { manifest { attributes 'Main-Class': 'demo.App' } }\n" >"$d/build.gradle"
  echo "rootProject.name = 'realapp'" >"$d/settings.gradle"
  cat >"$d/src/main/java/demo/App.java" <<'EOF'
package demo;
public class App {
    public static String greet(String n) { return "Hello, " + n; }
    public static void main(String[] args) { System.out.println(greet("jv")); }
}
EOF
  echo hello >"$d/src/main/resources/r.txt"
  rm -rf "$ref"; cp -R "$d" "$ref"
  (cd "$ref" && gradle -q --offline classes jar) >"$LOGDIR/gradle-ref.log" 2>&1 \
    || { skip "real Gradle" "gradle could not build the sample project offline (see $LOGDIR/gradle-ref.log)"; return; }

  runjv "$d" -o compile;                   t_rc0 "compile through the real Gradle model"
  t_out "asks Gradle for the model" 'asking Gradle for the project model'
  t_file "App.class" "$d/build/classes/java/main/demo/App.class"; t_file "resource" "$d/build/resources/main/r.txt"
  diff <(cd "$ref/build/classes" && find . -name '*.class' | sort) <(cd "$d/build/classes" && find . -name '*.class' | sort) >"$LOGDIR/gradle-classes.diff" 2>&1 \
    && ok "$CUR: same class files as gradle classes" || bad "$CUR: class files differ from gradle's" "see $LOGDIR/gradle-classes.diff"
  runjv "$d" -o compile;                   t_rc0 "second compile"; t_noout "model cached, gradle not started" 'asking Gradle'
  runjv "$d" -o package;                   t_rc0 "package"; t_file "jar" "$d/build/libs/realapp-1.0.jar"
  [ "$(java -jar "$d/build/libs/realapp-1.0.jar" 2>&1)" = "Hello, jv" ] && ok "$CUR: java -jar runs" || bad "$CUR: java -jar failed"
  runjv "$d" -o classpath compile;         t_rc0 "classpath"
  runjv "$d" clean;                        t_rc0 "clean"; t_nofile "build/ removed" "$d/build"
  runjv "$d" -o tasks;                     t_rc0 "an arbitrary task runs through gradle"
}

sc_sigterm() {
  begin sigterm
  local d="$WORK/sig" repo="$WORK/repo-sig"
  newproj "$d" "<dependencies>$(dep org.springframework.boot spring-boot-starter-web 3.2.2)</dependencies>"
  export JV_REPO="$repo"; rm -rf "$repo"
  (cd "$d" && exec "$JVBIN" compile >"$LOG.run" 2>&1) & local pid=$!
  sleep 0.7; kill -TERM "$pid" 2>/dev/null
  local t0 t1; t0=$(now); wait "$pid" 2>/dev/null; local rc=$?; t1=$(now)
  if [ "$rc" = 0 ]; then skip "$CUR: SIGTERM mid-download" "finished before the signal arrived"
  else
    [ "$(awk -v a="$t0" -v b="$t1" 'BEGIN{print (b-a < 10)}')" = 1 ] && ok "$CUR: exits promptly after SIGTERM (rc=$rc)" || bad "$CUR: slow to exit after SIGTERM"
    [ -z "$(find "$repo" -name '.jv-*' 2>/dev/null | head -1)" ] && ok "$CUR: no temp files left in the repo" || bad "$CUR: temp files left behind" "$(find "$repo" -name '.jv-*' | head -3)"
    [ -z "$(find "$repo" -name '*.jar' -size 0 2>/dev/null | head -1)" ] && ok "$CUR: no zero-byte jars left in the repo" || bad "$CUR: zero-byte jar left behind"
  fi
  unset JV_REPO
}

sc_concurrent() {
  begin concurrent
  local repo="$WORK/repo-conc" a="$WORK/conc-a" b="$WORK/conc-b"
  rm -rf "$repo"
  for d in "$a" "$b"; do newproj "$d" "<dependencies>$(dep org.springframework.boot spring-boot-starter-web 3.2.2)</dependencies>"; done
  export JV_REPO="$repo"
  (cd "$a" && "$JVBIN" -q classpath compile >"$a/out.txt" 2>"$a/err.txt"; echo $? >"$a/rc") &
  (cd "$b" && "$JVBIN" -q classpath compile >"$b/out.txt" 2>"$b/err.txt"; echo $? >"$b/rc") &
  wait
  unset JV_REPO
  [ "$(cat "$a/rc")" = 0 ] && [ "$(cat "$b/rc")" = 0 ] && ok "$CUR: two processes share a cold repo" || bad "$CUR: a concurrent run failed" "$(head -3 "$a/err.txt" "$b/err.txt")"
  cmp -s "$a/out.txt" "$b/out.txt" && ok "$CUR: both produced the same classpath" || bad "$CUR: classpaths differ between concurrent runs"
  local v; v=$(valid_jars "$(cat "$a/out.txt")") && ok "$CUR: every downloaded jar is a valid zip" || bad "$CUR: corrupt or missing jars" "$v"
}

graph() { # label g a v [heavy]
  local label=$1 g=$2 a=$3 v=$4 d="$WORK/graph-$1"
  newproj "$d" "<dependencies>$(dep "$g" "$a" "$v")</dependencies>"
  local t0 t1 n; t0=$(now); runjv "$d" classpath test; t1=$(now)
  if [ "$LAST_RC" != 0 ]; then bad "$CUR: $label resolves" "exit $LAST_RC; see $LOG"; return; fi
  n=$(cp_list "$LAST_OUT" | wc -l | tr -d ' ')
  ok "$CUR: $label resolves ($n jars, $(elapsed "$t0" "$t1")s cold)"
  local bj; bj=$(valid_jars "$LAST_OUT") && ok "$CUR: $label jars are valid" || bad "$CUR: $label has corrupt/missing jars" "$bj"
  parity "graph/$label" "$d"
}

sc_graphs() {
  begin graphs
  graph spring-boot-web org.springframework.boot spring-boot-starter-web 3.2.2
  graph maven-core org.apache.maven maven-core 3.9.6
  graph jackson-databind com.fasterxml.jackson.core jackson-databind 2.16.1
  graph httpclient org.apache.httpcomponents httpclient 4.5.14
  graph log4j-core org.apache.logging.log4j log4j-core 2.22.1
  if [ "$TIER" = full ]; then
    graph hadoop-common org.apache.hadoop hadoop-common 3.3.6
    graph hibernate-core org.hibernate.orm hibernate-core 6.4.2.Final
    graph netty-all io.netty netty-all 4.1.106.Final
    graph aws-s3-v1 com.amazonaws aws-java-sdk-s3 1.12.640
  fi
}

# ---------------------------------------------------------------- corpus of real projects
# name|git url|ref|subdir|mode      mode: cp = classpath parity only, build = cp parity + compile parity
# NOTE: refs are best-effort; a clone failure is reported as SKIP, so prune or fix lines freely.
BUILTIN_CORPUS='commons-lang|https://github.com/apache/commons-lang|rel/commons-lang-3.14.0|.|build
commons-io|https://github.com/apache/commons-io|rel/commons-io-2.15.1|.|build
commons-codec|https://github.com/apache/commons-codec|rel/commons-codec-1.16.0|.|build
joda-time|https://github.com/JodaOrg/joda-time|v2.12.7|.|build
jackson-core|https://github.com/FasterXML/jackson-core|jackson-core-2.16.1|.|cp
jackson-databind|https://github.com/FasterXML/jackson-databind|jackson-databind-2.16.1|.|cp
gson|https://github.com/google/gson|gson-parent-2.10.1|gson|cp
jsoup|https://github.com/jhy/jsoup|jsoup-1.17.2|.|cp
guice-core|https://github.com/google/guice|7.0.0|core|cp
spring-petclinic|https://github.com/spring-projects/spring-petclinic|main|.|cp'

corpus_item() {
  local name=$1 url=$2 ref=$3 sub=$4 mode=$5
  local CUR="corpus/$name" dir="$WORK/corpus/$1" proj
  if [ "$HAVE_GIT" = 0 ]; then skip "$CUR" "git not installed"; return; fi
  mkdir -p "$WORK/corpus"
  if [ ! -d "$dir/.git" ]; then
    git clone -q --depth 1 --branch "$ref" "$url" "$dir" >"$LOGDIR/clone-$name.log" 2>&1 || { skip "$CUR" "clone failed for ref '$ref' (see $LOGDIR/clone-$name.log)"; return; }
  fi
  proj="$dir/$sub"
  [ -f "$proj/pom.xml" ] || { skip "$CUR" "no pom.xml in $sub"; return; }
  parity "$CUR" "$proj"
  [ "$mode" = build ] || [ "$RUN_TESTS" = 1 ] || return 0
  if [ "$HAVE_JAVAC" = 0 ]; then skip "$CUR: compile parity" "no javac"; return; fi
  if [ "$HAVE_MVN" = 0 ]; then
    rm -rf "$proj/target" "$proj/.jv"; (cd "$proj" && "$JVBIN" -q compile) >"$LOGDIR/corpus-$name-jv.log" 2>&1 \
      && ok "$CUR: jv compiles" || bad "$CUR: jv compile failed" "see $LOGDIR/corpus-$name-jv.log"
    return
  fi
  rm -rf "$proj/target" "$proj/.jv" "$proj/target.mvn"
  local t0 t1 t2 t3 t4
  t0=$(now); mvn -q -B -ntp -f "$proj/pom.xml" compile >"$LOGDIR/corpus-$name-mvn.log" 2>&1; local mrc=$?; t1=$(now)
  if [ $mrc -ne 0 ]; then skip "$CUR: compile parity" "mvn compile itself fails here (see $LOGDIR/corpus-$name-mvn.log)"; return; fi
  local mcount; mcount=$(find "$proj/target/classes" -name '*.class' | wc -l | tr -d ' ')
  mv "$proj/target" "$proj/target.mvn"
  t2=$(now); (cd "$proj" && "$JVBIN" -q compile) >"$LOGDIR/corpus-$name-jv.log" 2>&1; local jrc=$?; t3=$(now)
  if [ $jrc -ne 0 ]; then bad "$CUR: jv compile failed where mvn succeeded" "see $LOGDIR/corpus-$name-jv.log"; return; fi
  local jcount; jcount=$(find "$proj/target/classes" -name '*.class' | wc -l | tr -d ' ')
  [ "$mcount" = "$jcount" ] && ok "$CUR: compiled class count matches mvn ($jcount)" || bad "$CUR: class count differs" "mvn=$mcount jv=$jcount"
  (cd "$proj" && "$JVBIN" -q compile) >/dev/null 2>&1; t4=$(now)
  TIMES="$TIMES"$'\n'"$(printf '  %-22s cold: jv %6ss   mvn %6ss   | warm (no-op): jv %5ss' "$name" "$(elapsed "$t2" "$t3")" "$(elapsed "$t0" "$t1")" "$(elapsed "$t3" "$t4")")"
  rm -rf "$proj/target.mvn"
  if [ "$RUN_TESTS" = 1 ]; then
    (cd "$proj" && "$JVBIN" -q test) >"$LOGDIR/corpus-$name-jvtest.log" 2>&1; local jt=$?
    mvn -q -B -ntp -f "$proj/pom.xml" test >"$LOGDIR/corpus-$name-mvntest.log" 2>&1; local mt=$?
    if [ "$jt" = 0 ] && [ "$mt" = 0 ]; then ok "$CUR: tests pass under both"
    elif [ "$jt" != 0 ] && [ "$mt" != 0 ]; then skip "$CUR: tests" "fail under both (see logs)"
    else bad "$CUR: test results differ" "jv rc=$jt, mvn rc=$mt; see $LOGDIR/corpus-$name-*test.log"; fi
  fi
}

sc_corpus() {
  section "Corpus of real projects ($( [ "$HAVE_MVN" = 1 ] && echo 'parity vs mvn' || echo 'no mvn: jv-only checks' ))"
  local list; if [ -n "$CORPUS_FILE" ]; then list=$(cat "$CORPUS_FILE"); else list=$BUILTIN_CORPUS; fi
  local name url ref sub mode
  while IFS='|' read -r name url ref sub mode; do
    [ -z "$name" ] && continue
    case "$name" in \#*) continue ;; esac
    want "corpus/$name" || want corpus || continue
    corpus_item "$name" "$url" "$ref" "$sub" "${mode:-cp}"
  done <<EOF
$list
EOF
}

# ---------------------------------------------------------------- run
if [ "$SKIP_E2E" = 0 ]; then
  if [ "$HAVE_JAVAC" = 0 ] && [ "$TIER" != corpus ]; then
    section "End-to-end scenarios"; skip "all scenarios" "javac not found (install a JDK 17+)"
  else
    check_network
    if [ "$NET_OK" = 0 ]; then
      section "End-to-end scenarios"; skip "all network scenarios and corpus" "cannot reach repo1.maven.org (use --quick for offline-only checks)"
    else
      if [ "$TIER" != corpus ]; then
        for s in basic single failing_test compile_error scopes ranges profiles reactor offline errors fallback gradle gradle_real sigterm concurrent graphs; do
          want "$s" && "sc_$s"
        done
      fi
      if [ "$TIER" = full ] || [ "$TIER" = corpus ]; then sc_corpus; fi
    fi
  fi
fi

# ---------------------------------------------------------------- summary
section "Summary"
[ -n "$TIMES" ] && { echo "  Timings (compile of real projects):"; printf '%s\n' "$TIMES"; echo; }
printf '  %s%d passed%s, %s%d failed%s, %s%d skipped%s, %d warnings\n' "$GRN" "$PASS" "$RST" "$RED" "$FAIL" "$RST" "$YEL" "$SKIP" "$RST" "$WARN"
if [ "$FAIL" -gt 0 ]; then
  printf '%s\n  Failures:%s\n' "$RED" "$RST"; printf '%s\n' "$FAILED"
  echo; echo "  Logs and project dirs kept in: $WORK"
  exit 1
fi
[ "$HAVE_MVN" = 0 ] && [ "$TIER" != quick ] && echo "  (parity checks were skipped: install Maven and re-run for the strongest guarantee)"
exit 0
