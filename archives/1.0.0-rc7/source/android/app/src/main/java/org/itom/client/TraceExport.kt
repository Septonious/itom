package org.itom.client

import java.io.File
import java.io.FileInputStream
import java.io.OutputStream
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

// Open all trace generations before streaming: rotation cannot replace an open inode.
// Snapshot sizes bound the export even while the current trace is still growing.
internal fun exportTransportTrace(root: File, profile: String?, report: String, output: OutputStream) {
    val sources = mutableListOf<Triple<String, FileInputStream, Long>>()
    try {
        if (profile != null) {
            require(Regex("[A-Za-z0-9_-]{1,64}").matches(profile))
            val base = File(root, "transport").canonicalFile
            val directory = File(base, "$profile/transport").canonicalFile
            require(directory.path.startsWith(base.path + File.separator))
            for (name in listOf("transport-trace.jsonl.2", "transport-trace.jsonl.1", "transport-trace.jsonl")) {
                val file = File(directory, name)
                require(file.canonicalFile.parentFile == directory)
                val stream = try { FileInputStream(file) } catch (e: java.io.FileNotFoundException) {
                    if (file.exists()) throw e else continue
                }
                sources += Triple(name, stream, stream.channel.size().coerceAtMost(16L * 1024 * 1024))
            }
        }
        ZipOutputStream(output).use { zip ->
            zip.putNextEntry(ZipEntry("diagnostics.json"))
            zip.write(report.toByteArray(Charsets.UTF_8)); zip.closeEntry()
            zip.putNextEntry(ZipEntry("capture.json"))
            zip.write("""{"schema":1,"live_snapshot_best_effort":true,"trace_files":${sources.size},"plaintext_payload":false}""".toByteArray(Charsets.UTF_8))
            zip.closeEntry()
            val buffer = ByteArray(64 * 1024)
            for ((name, stream, size) in sources) {
                zip.putNextEntry(ZipEntry(name))
                var remaining = size
                while (remaining > 0) {
                    val n = stream.read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt())
                    if (n < 0) break
                    zip.write(buffer, 0, n); remaining -= n
                }
                zip.closeEntry()
            }
        }
    } finally {
        sources.forEach { runCatching { it.second.close() } }
    }
}
