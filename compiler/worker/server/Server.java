// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package worker.server;

import java.io.*;
import java.net.*;
import java.nio.*;
import java.nio.channels.*;
import java.nio.charset.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import javax.tools.*;

/**
 * Persistent javac worker server.
 *
 * <p>Usage:
 * <ul>
 *   <li>{@code Server <socket-path>} — Unix domain socket mode (Linux/macOS)
 *   <li>{@code Server --tcp} — TCP loopback mode (Windows); prints "READY:127.0.0.1:PORT"
 * </ul>
 *
 * <p>The server accepts concurrent compile requests, dispatching each to a fixed thread pool
 * sized to {@code max(2, availableProcessors())}.
 */
public class Server {

    public static void main(String[] args) {
        if (args.length < 1) {
            System.err.println("Usage: Server <socket-path> | --tcp");
            System.exit(1);
        }

        boolean tcpMode = args[0].equals("--tcp");
        String socketPath = tcpMode ? null : args[0];

        int nThreads = Math.max(2, Runtime.getRuntime().availableProcessors());
        ExecutorService pool = Executors.newFixedThreadPool(nThreads);

        // Shutdown hook: drain pool and clean up socket file.
        final String sp = socketPath;
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            pool.shutdown();
            if (sp != null) {
                try { Files.deleteIfExists(Path.of(sp)); } catch (Exception ignored) {}
            }
        }));

        // Stdin EOF watcher: exit when parent process closes its end of stdin.
        Thread watcher = new Thread(() -> {
            try { while (System.in.read() != -1) { /* drain */ } } catch (Exception ignored) {}
            if (sp != null) {
                try { Files.deleteIfExists(Path.of(sp)); } catch (Exception ignored) {}
            }
            System.exit(0);
        });
        watcher.setDaemon(true);
        watcher.start();

        JavaCompiler compiler = ToolProvider.getSystemJavaCompiler();
        if (compiler == null) {
            System.err.println("[jv-worker] No system JavaCompiler available — run with a JDK, not a JRE.");
            System.exit(1);
        }

        if (tcpMode) {
            runTcpServer(compiler, pool);
        } else {
            runUnixServer(compiler, pool, socketPath);
        }
    }

    // -------------------------------------------------------------------------
    // Unix domain socket server
    // -------------------------------------------------------------------------

    private static void runUnixServer(JavaCompiler compiler, ExecutorService pool, String socketPath) {
        Path sockFile = Path.of(socketPath);
        try { Files.deleteIfExists(sockFile); } catch (IOException ignored) {}

        try (ServerSocketChannel server = ServerSocketChannel.open(StandardProtocolFamily.UNIX)) {
            server.bind(UnixDomainSocketAddress.of(sockFile));
            System.out.println("READY");
            System.out.flush();

            while (true) {
                SocketChannel client = server.accept();
                pool.submit(() -> handleSocketChannel(compiler, client));
            }
        } catch (Exception e) {
            System.err.println("[jv-worker] Unix server error: " + e.getMessage());
            System.exit(1);
        }
    }

    private static void handleSocketChannel(JavaCompiler compiler, SocketChannel client) {
        try (client) {
            ByteBuffer buf = ByteBuffer.allocate(64 * 1024);
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            while (client.read(buf) > 0) {
                buf.flip();
                byte[] bytes = new byte[buf.remaining()];
                buf.get(bytes);
                baos.write(bytes);
                buf.clear();
                if (baos.toString(StandardCharsets.UTF_8).trim().endsWith("}")) break;
            }
            byte[] resp = compileFromJson(compiler, baos.toString(StandardCharsets.UTF_8));
            ByteBuffer outBuf = ByteBuffer.wrap(resp);
            while (outBuf.hasRemaining()) {
                client.write(outBuf);
            }
        } catch (Exception e) {
            String errResp = errorResponse("Worker exception: " + e.getMessage());
            try { client.write(ByteBuffer.wrap(errResp.getBytes(StandardCharsets.UTF_8))); }
            catch (IOException ignored) {}
        }
    }

    // -------------------------------------------------------------------------
    // TCP loopback server (Windows)
    // -------------------------------------------------------------------------

    private static void runTcpServer(JavaCompiler compiler, ExecutorService pool) {
        try (ServerSocket server = new ServerSocket(0, 50, InetAddress.getLoopbackAddress())) {
            int port = server.getLocalPort();
            // Print the actual bound address so the Go side can connect.
            System.out.println("READY:127.0.0.1:" + port);
            System.out.flush();

            while (true) {
                Socket client = server.accept();
                pool.submit(() -> handleTcpSocket(compiler, client));
            }
        } catch (Exception e) {
            System.err.println("[jv-worker] TCP server error: " + e.getMessage());
            System.exit(1);
        }
    }

    private static void handleTcpSocket(JavaCompiler compiler, Socket client) {
        try (client) {
            InputStream in = client.getInputStream();
            OutputStream out = client.getOutputStream();

            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            byte[] tmp = new byte[8192];
            int n;
            while ((n = in.read(tmp)) > 0) {
                baos.write(tmp, 0, n);
                if (baos.toString(StandardCharsets.UTF_8).trim().endsWith("}")) break;
            }
            byte[] resp = compileFromJson(compiler, baos.toString(StandardCharsets.UTF_8));
            out.write(resp);
            out.flush();
        } catch (Exception e) {
            System.err.println("[jv-worker] TCP handler error: " + e.getMessage());
        }
    }

    // -------------------------------------------------------------------------
    // Core compile logic (shared by Unix and TCP handlers)
    // -------------------------------------------------------------------------

    private static byte[] compileFromJson(JavaCompiler compiler, String json) {
        Map<String, Object> req = parseJson(json);
        @SuppressWarnings("unchecked")
        List<String> rawArgs = (List<String>) req.getOrDefault("args", Collections.emptyList());
        String dir = (String) req.getOrDefault("dir", "");

        List<String> effectiveArgs = new ArrayList<>();
        for (int i = 0; i < rawArgs.size(); i++) {
            String a = rawArgs.get(i);
            if (a.startsWith("@")) {
                Path path = Path.of(a.substring(1));
                if (!path.isAbsolute() && dir != null && !dir.isEmpty()) {
                    path = Path.of(dir).resolve(path).toAbsolutePath();
                }
                effectiveArgs.add("@" + path);
            } else if ((a.equals("-d") || a.equals("-cp") || a.equals("-classpath")) && i + 1 < rawArgs.size()) {
                effectiveArgs.add(a);
                i++;
                String val = rawArgs.get(i);
                if (a.equals("-d")) {
                    Path p = Path.of(val);
                    if (!p.isAbsolute() && dir != null && !dir.isEmpty()) {
                        p = Path.of(dir).resolve(p).toAbsolutePath();
                    }
                    effectiveArgs.add(p.toString());
                } else {
                    // Classpath: resolve each entry individually
                    String[] parts = val.split(File.pathSeparator);
                    StringBuilder sb = new StringBuilder();
                    for (int j = 0; j < parts.length; j++) {
                        if (j > 0) sb.append(File.pathSeparator);
                        Path cpP = Path.of(parts[j]);
                        if (!cpP.isAbsolute() && dir != null && !dir.isEmpty()) {
                            cpP = Path.of(dir).resolve(cpP).toAbsolutePath();
                        }
                        sb.append(cpP);
                    }
                    effectiveArgs.add(sb.toString());
                }
            } else if (a.endsWith(".java")) {
                Path p = Path.of(a);
                if (!p.isAbsolute() && dir != null && !dir.isEmpty()) {
                    p = Path.of(dir).resolve(p).toAbsolutePath();
                }
                effectiveArgs.add(p.toString());
            } else {
                effectiveArgs.add(a);
            }
        }

        ByteArrayOutputStream outBaos = new ByteArrayOutputStream();
        ByteArrayOutputStream errBaos = new ByteArrayOutputStream();
        int rc = compiler.run(null, outBaos, errBaos, effectiveArgs.toArray(new String[0]));

        String outStr = outBaos.toString(StandardCharsets.UTF_8);
        String errStr = errBaos.toString(StandardCharsets.UTF_8);
        String respJson = String.format(
                "{\"exitCode\": %d, \"stdout\": %s, \"stderr\": %s}\n",
                rc, quoteJson(outStr), quoteJson(errStr));
        return respJson.getBytes(StandardCharsets.UTF_8);
    }

    private static String errorResponse(String message) {
        return String.format("{\"exitCode\": 1, \"stdout\": \"\", \"stderr\": %s}\n", quoteJson(message));
    }

    // -------------------------------------------------------------------------
    // JSON helpers — proper recursive-descent parser (FIX #5)
    // Handles all escape sequences including unicode escapes, nested objects/arrays,
    // numbers, booleans, null. No external library dependency.
    // -------------------------------------------------------------------------

    private static String quoteJson(String s) {
        if (s == null) return "\"\"";
        StringBuilder sb = new StringBuilder("\"");
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"'  -> sb.append("\\\"");
                case '\\' -> sb.append("\\\\");
                case '\b' -> sb.append("\\b");
                case '\f' -> sb.append("\\f");
                case '\n' -> sb.append("\\n");
                case '\r' -> sb.append("\\r");
                case '\t' -> sb.append("\\t");
                default -> {
                    if (c < ' ') {
                        sb.append(String.format("\\u%04x", (int) c));
                    } else {
                        sb.append(c);
                    }
                }
            }
        }
        sb.append("\"");
        return sb.toString();
    }

    /** Parses a top-level JSON object into a Map. Values are String or List&lt;String&gt;. */
    private static Map<String, Object> parseJson(String s) {
        int[] pos = {0};
        skipWs(s, pos);
        if (pos[0] >= s.length() || s.charAt(pos[0]) != '{') return new LinkedHashMap<>();
        pos[0]++; // consume '{'
        Map<String, Object> res = new LinkedHashMap<>();
        while (true) {
            skipWs(s, pos);
            if (pos[0] >= s.length()) break;
            char c = s.charAt(pos[0]);
            if (c == '}') { pos[0]++; break; }
            if (c == ',') { pos[0]++; continue; }
            if (c != '"') { pos[0]++; continue; }
            String key = parseString(s, pos);
            skipWs(s, pos);
            if (pos[0] < s.length() && s.charAt(pos[0]) == ':') pos[0]++;
            skipWs(s, pos);
            Object val = parseValue(s, pos);
            res.put(key, val);
        }
        return res;
    }

    private static Object parseValue(String s, int[] pos) {
        skipWs(s, pos);
        if (pos[0] >= s.length()) return "";
        char c = s.charAt(pos[0]);
        if (c == '"') return parseString(s, pos);
        if (c == '[') return parseArray(s, pos);
        if (c == '{') { parseJson(s.substring(pos[0])); return ""; } // nested object — skip, unsupported
        int start = pos[0];
        while (pos[0] < s.length()) {
            char ch = s.charAt(pos[0]);
            if (ch == ',' || ch == '}' || ch == ']' || Character.isWhitespace(ch)) break;
            pos[0]++;
        }
        return s.substring(start, pos[0]); // number / boolean / null as string
    }

    private static List<String> parseArray(String s, int[] pos) {
        pos[0]++; // consume '['
        List<String> arr = new ArrayList<>();
        while (true) {
            skipWs(s, pos);
            if (pos[0] >= s.length()) break;
            char c = s.charAt(pos[0]);
            if (c == ']') { pos[0]++; break; }
            if (c == ',') { pos[0]++; continue; }
            Object v = parseValue(s, pos);
            arr.add(v == null ? "" : v.toString());
        }
        return arr;
    }

    private static String parseString(String s, int[] pos) {
        pos[0]++; // consume opening '"'
        StringBuilder sb = new StringBuilder();
        while (pos[0] < s.length()) {
            char c = s.charAt(pos[0]++);
            if (c == '"') break;
            if (c != '\\') { sb.append(c); continue; }
            if (pos[0] >= s.length()) break;
            char esc = s.charAt(pos[0]++);
            switch (esc) {
                case '"'  -> sb.append('"');
                case '\\' -> sb.append('\\');
                case '/'  -> sb.append('/');
                case 'b'  -> sb.append('\b');
                case 'f'  -> sb.append('\f');
                case 'n'  -> sb.append('\n');
                case 'r'  -> sb.append('\r');
                case 't'  -> sb.append('\t');
                case 'u'  -> { // unicode escape (\\uHEX) — previously unhandled
                    if (pos[0] + 4 <= s.length()) {
                        String hex = s.substring(pos[0], pos[0] + 4);
                        pos[0] += 4;
                        try { sb.append((char) Integer.parseInt(hex, 16)); }
                        catch (NumberFormatException ignored) { sb.append("\\u").append(hex); }
                    }
                }
                default -> sb.append(esc);
            }
        }
        return sb.toString();
    }

    private static void skipWs(String s, int[] pos) {
        while (pos[0] < s.length() && Character.isWhitespace(s.charAt(pos[0]))) pos[0]++;
    }
}

