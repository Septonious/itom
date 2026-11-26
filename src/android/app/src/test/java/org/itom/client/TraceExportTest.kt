package org.itom.client

import java.io.ByteArrayOutputStream
import java.nio.file.Files
import java.util.zip.ZipInputStream
import kotlin.test.*
import org.junit.Test

class TraceExportTest {
    @Test fun immediateFailureExportsStartupTraceWithoutRuntimeTraceOrClient() {
        val root = Files.createTempDirectory("itom-startup-export").toFile()
        try {
            val directory = root.resolve("transport/profile1/transport").apply { mkdirs() }
            val trace = """{"Event":"client_startup","Stage":"REQUEST","FailureReason":"IMAP_TAGGED_REFUSAL"}""" + "\n"
            directory.resolve("startup-trace.jsonl").writeText(trace)
            val output = ByteArrayOutputStream()
            exportTransportTrace(root, "profile1", "{}", output)
            val entries = mutableMapOf<String, String>()
            ZipInputStream(output.toByteArray().inputStream()).use { zip ->
                while (true) {
                    val entry = zip.nextEntry ?: break
                    entries[entry.name] = zip.readBytes().toString(Charsets.UTF_8)
                }
            }
            assertEquals(trace, entries["startup-trace.jsonl"])
            assertEquals("{}", entries["diagnostics.json"])
            assertTrue(entries.getValue("capture.json").contains("\"startup_trace_files\":1"))
            assertTrue(entries.getValue("capture.json").contains("\"transport_trace_files\":0"))
            assertFalse(entries.containsKey("transport-trace.jsonl"))
        } finally { root.deleteRecursively() }
    }
    @Test fun multipleRotatedTraceGenerationsSurviveNetworkHandover() {
        val root = Files.createTempDirectory("itom-handover-trace").toFile()
        try {
            val directory = root.resolve("transport/profile1/transport").apply { mkdirs() }
            directory.resolve("transport-trace.jsonl.6").writeText("before-wifi\n")
            directory.resolve("transport-trace.jsonl.3").writeText("on-mobile\n")
            directory.resolve("transport-trace.jsonl").writeText("after-wifi\n")
            val output = ByteArrayOutputStream()
            exportTransportTrace(root, "profile1", "{}", output)
            val names = mutableListOf<String>()
            ZipInputStream(output.toByteArray().inputStream()).use { zip ->
                while (true) {
                    val entry = zip.nextEntry ?: break
                    names += entry.name
                    zip.readBytes()
                }
            }
            assertTrue(names.indexOf("transport-trace.jsonl.6") < names.indexOf("transport-trace.jsonl.3"))
            assertTrue(names.indexOf("transport-trace.jsonl.3") < names.indexOf("transport-trace.jsonl"))
        } finally { root.deleteRecursively() }
    }
}
