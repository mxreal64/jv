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
import javax.tools.*;

public class Server {
    public static void main(String[] args) {
        if (args.length < 1) {
            System.err.println("Usage: Server <socket-path>");
            System.exit(1);
        }
        String socketPath = args[0];
        Path sockFile = Path.of(socketPath);

        try {
            Files.deleteIfExists(sockFile);
        } catch (IOException ignored) {}

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            try {
                Files.deleteIfExists(sockFile);
            } catch (Exception ignored) {}
        }));

        // Thread to detect EOF on stdin and exit
        Thread watcher = new Thread(() -> {
            try {
                int r;
                while ((r = System.in.read()) != -1) {
                    // keep reading until EOF
                }
            } catch (Exception ignored) {}
            try {
                Files.deleteIfExists(sockFile);
            } catch (Exception ignored) {}
            System.exit(0);
        });
        watcher.setDaemon(true);
        watcher.start();

        JavaCompiler compiler = ToolProvider.getSystemJavaCompiler();
        if (compiler == null) {
            System.err.println("No system JavaCompiler available");
            System.exit(1);
        }

        try (ServerSocketChannel server = ServerSocketChannel.open(StandardProtocolFamily.UNIX)) {
            server.bind(UnixDomainSocketAddress.of(sockFile));
            System.out.println("READY");
            System.out.flush();

            while (true) {
                SocketChannel client = server.accept();
                handleClient(compiler, client);
            }
        } catch (Exception e) {
            e.printStackTrace();
            System.exit(1);
        }
    }

    private static void handleClient(JavaCompiler compiler, SocketChannel client) {
        try (client) {
            ByteBuffer buf = ByteBuffer.allocate(64 * 1024);
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            while (client.read(buf) > 0) {
                buf.flip();
                byte[] bytes = new byte[buf.remaining()];
                buf.get(bytes);
                baos.write(bytes);
                buf.clear();
                // If we encounter a newline or closing brace at end of message, break if complete JSON
                String str = baos.toString(StandardCharsets.UTF_8);
                if (str.trim().endsWith("}")) {
                    break;
                }
            }

            String json = baos.toString(StandardCharsets.UTF_8);
            Map<String, Object> req = parseJson(json);
            @SuppressWarnings("unchecked")
            List<String> rawArgs = (List<String>) req.getOrDefault("args", Collections.emptyList());
            String dir = (String) req.getOrDefault("dir", "");

            List<String> effectiveArgs = new ArrayList<>();
            for (int i = 0; i < rawArgs.size(); i++) {
                String a = rawArgs.get(i);
                if (a.startsWith("@")) {
                    String p = a.substring(1);
                    Path path = Path.of(p);
                    if (!path.isAbsolute() && dir != null && !dir.isEmpty()) {
                        path = Path.of(dir).resolve(path).toAbsolutePath();
                    }
                    effectiveArgs.add("@" + path.toString());
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
                        // classpath
                        String[] parts = val.split(File.pathSeparator);
                        StringBuilder sb = new StringBuilder();
                        for (int j = 0; j < parts.length; j++) {
                            if (j > 0) sb.append(File.pathSeparator);
                            Path cpP = Path.of(parts[j]);
                            if (!cpP.isAbsolute() && dir != null && !dir.isEmpty()) {
                                cpP = Path.of(dir).resolve(cpP).toAbsolutePath();
                            }
                            sb.append(cpP.toString());
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

            String respJson = String.format("{\"exitCode\": %d, \"stdout\": %s, \"stderr\": %s}\n",
                    rc, quoteJson(outStr), quoteJson(errStr));

            byte[] respBytes = respJson.getBytes(StandardCharsets.UTF_8);
            ByteBuffer outBuf = ByteBuffer.wrap(respBytes);
            while (outBuf.hasRemaining()) {
                client.write(outBuf);
            }
        } catch (Exception e) {
            String errResp = String.format("{\"exitCode\": 1, \"stdout\": \"\", \"stderr\": %s}\n",
                    quoteJson("Worker exception: " + e.getMessage()));
            try {
                client.write(ByteBuffer.wrap(errResp.getBytes(StandardCharsets.UTF_8)));
            } catch (IOException ignored) {}
        }
    }

    private static String quoteJson(String s) {
        if (s == null) return "\"\"";
        StringBuilder sb = new StringBuilder("\"");
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"' -> sb.append("\\\"");
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

    private static Map<String, Object> parseJson(String s) {
        Map<String, Object> res = new HashMap<>();
        s = s.trim();
        if (s.startsWith("{")) s = s.substring(1);
        if (s.endsWith("}")) s = s.substring(0, s.length() - 1);
        int i = 0;
        while (i < s.length()) {
            while (i < s.length() && (Character.isWhitespace(s.charAt(i)) || s.charAt(i) == ',')) i++;
            if (i >= s.length()) break;
            if (s.charAt(i) != '"') break;
            int keyStart = ++i;
            while (i < s.length() && s.charAt(i) != '"') i++;
            String key = s.substring(keyStart, i++);
            while (i < s.length() && (Character.isWhitespace(s.charAt(i)) || s.charAt(i) == ':')) i++;
            if (i >= s.length()) break;
            if (s.charAt(i) == '"') {
                int valStart = ++i;
                StringBuilder sb = new StringBuilder();
                while (i < s.length()) {
                    char c = s.charAt(i);
                    if (c == '\\' && i + 1 < s.length()) {
                        sb.append(s.charAt(++i));
                        i++;
                    } else if (c == '"') {
                        i++;
                        break;
                    } else {
                        sb.append(c);
                        i++;
                    }
                }
                res.put(key, sb.toString());
            } else if (s.charAt(i) == '[') {
                i++;
                List<String> arr = new ArrayList<>();
                while (i < s.length()) {
                    while (i < s.length() && (Character.isWhitespace(s.charAt(i)) || s.charAt(i) == ',')) i++;
                    if (i < s.length() && s.charAt(i) == ']') {
                        i++;
                        break;
                    }
                    if (i < s.length() && s.charAt(i) == '"') {
                        i++;
                        StringBuilder sb = new StringBuilder();
                        while (i < s.length()) {
                            char c = s.charAt(i);
                            if (c == '\\' && i + 1 < s.length()) {
                                sb.append(s.charAt(++i));
                                i++;
                            } else if (c == '"') {
                                i++;
                                break;
                            } else {
                                sb.append(c);
                                i++;
                            }
                        }
                        arr.add(sb.toString());
                    } else {
                        i++;
                    }
                }
                res.put(key, arr);
            } else {
                i++;
            }
        }
        return res;
    }
}
