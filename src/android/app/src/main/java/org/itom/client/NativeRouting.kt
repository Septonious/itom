package org.itom.client

import android.content.Context
import android.util.AtomicFile
import java.net.URL
import javax.net.ssl.HttpsURLConnection
import java.security.MessageDigest
import java.util.UUID
import java.text.DateFormat
import java.util.Date
import org.itom.domain.*
import org.itom.nativecore.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** The APK bundles the byte-exact RC20 routing data. Never activate a partial
 * snapshot. Native validation is performed before the directory is published. */
object NativeRouting {
    private val names = listOf("geosite.dat", "geoip.dat", "ads-trackers.txt", "malware.txt")
    private var validated: File? = null

    @Synchronized fun prepare(context: Context): File {
        val root = File(context.noBackupFilesDir, "routing-data")
        val pointer = AtomicFile(File(root, "active-snapshot"))
        val name = runCatching { pointer.readFully().toString(Charsets.UTF_8) }.getOrNull()
        if (name != null && name.matches(Regex("updated-[a-f0-9-]{36}"))) {
            val candidate = File(root, name)
            if (validated == candidate && names.all { File(candidate, it).isFile }) return candidate
            if (candidate.isDirectory && runCatching { Mobile.validateRoutingData(candidate.absolutePath) }.isSuccess) {
                validated = candidate
                return candidate
            }
        }
        val active = File(root, "rc20")
        validated?.takeIf { it == active && names.all { name -> File(it, name).isFile } }?.let { return it }
        if (active.isDirectory) {
            if (runCatching { Mobile.validateRoutingData(active.absolutePath) }.isSuccess) {
                validated = active
                return active
            }
        }
        val parent = requireNotNull(active.parentFile)
        parent.mkdirs()
        val pending = File(parent, "rc20-pending")
        pending.deleteRecursively()
        demand(pending.mkdir(), "ROUTING_DATA_STORAGE")
        try {
            for (name in names) {
                context.assets.open("routing-data/$name").use { input ->
                    File(pending, name).outputStream().use { output ->
                        val buffer = ByteArray(64 * 1024)
                        var total = 0L
                        while (true) {
                            val size = input.read(buffer)
                            if (size < 0) break
                            total += size
                            demand(total <= 64L * 1024 * 1024, "ROUTING_DATA_SIZE")
                            output.write(buffer, 0, size)
                        }
                        demand(total > 0, "ROUTING_DATA_EMPTY")
                    }
                }
            }
            context.assets.open("routing-data/manifest.json").use { input -> File(pending, "manifest.json").outputStream().use { input.copyTo(it) } }
            Mobile.validateRoutingData(pending.absolutePath)
            if (active.exists()) demand(active.deleteRecursively(), "ROUTING_DATA_STORAGE")
            demand(pending.renameTo(active), "ROUTING_DATA_STORAGE")
            validated = active
            return active
        } finally { if (pending.exists()) pending.deleteRecursively() }
    }

    fun updatedLabel(context: Context): String = runCatching {
        val active = prepare(context)
        val manifest = File(active, "manifest.json")
        val raw = if (manifest.isFile) manifest.readText() else context.assets.open("routing-data/manifest.json").bufferedReader().use { it.readText() }
        val seconds = JSONObject(raw).getLong("updated_at")
        DateFormat.getDateInstance(DateFormat.MEDIUM).format(Date(seconds * 1000))
    }.getOrDefault("Bundled routing data")

    private fun <T> fetch(url: String, read: (java.io.InputStream) -> T): T {
        val conn = URL(url).openConnection() as HttpsURLConnection
        conn.connectTimeout = 15000
        conn.readTimeout = 30000
        conn.instanceFollowRedirects = false
        conn.setRequestProperty("User-Agent", "ITOM-routing-update")
        try {
            demand(conn.responseCode == 200, "ROUTING_DATA_DOWNLOAD")
            return conn.inputStream.use(read)
        } finally { conn.disconnect() }
    }
    private fun commit(repo: String, branch: String): String {
        val sha = fetch("https://api.github.com/repos/$repo/commits/$branch") { input ->
            val out = java.io.ByteArrayOutputStream()
            val buffer = ByteArray(8192)
            while (true) { val n = input.read(buffer); if (n < 0) break; demand(out.size() + n <= 1024 * 1024, "ROUTING_DATA_SIZE"); out.write(buffer, 0, n) }
            JSONObject(out.toString("UTF-8")).getString("sha")
        }
        demand(sha.matches(Regex("[a-f0-9]{40}")), "ROUTING_DATA_VERSION")
        return sha
    }

    /** Download a coherent snapshot at immutable upstream commits. Never change
     * the live directory or publish a partial/invalid set. Running sessions keep
     * their original routing snapshot; the pointer applies to the next start. */
    @Synchronized fun updateLists(context: Context, policy: RoutingPolicy) {
        val root = File(context.noBackupFilesDir, "routing-data")
        prepare(context)
        val geo = "Loyalsoldier/v2ray-rules-dat"
        val lists = "hagezi/dns-blocklists"
        val geoVersion = commit(geo, "release")
        val listVersion = commit(lists, "main")
        val urls = mapOf(
            "geosite.dat" to "https://raw.githubusercontent.com/$geo/$geoVersion/geosite.dat",
            "geoip.dat" to "https://raw.githubusercontent.com/$geo/$geoVersion/geoip.dat",
            "ads-trackers.txt" to "https://raw.githubusercontent.com/$lists/$listVersion/wildcard/pro-onlydomains.txt",
            "malware.txt" to "https://raw.githubusercontent.com/$lists/$listVersion/wildcard/tif.medium-onlydomains.txt")
        val pending = File(root, "updated-${UUID.randomUUID()}")
        demand(pending.mkdir(), "ROUTING_DATA_STORAGE")
        var published = false
        try {
            val datasets = JSONObject()
            for ((name, url) in urls) {
                val digest = MessageDigest.getInstance("SHA-256")
                var total = 0L
                fetch(url) { input -> File(pending, name).outputStream().use { out ->
                    val buffer = ByteArray(65536)
                    while (true) {
                        val n = input.read(buffer); if (n < 0) break
                        total += n
                        demand(total <= 64L * 1024 * 1024, "ROUTING_DATA_SIZE")
                        digest.update(buffer, 0, n); out.write(buffer, 0, n)
                    }
                } }
                demand(total > 0, "ROUTING_DATA_EMPTY")
                datasets.put(name, JSONObject().put("url", url).put("size", total).put("sha256", digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }))
            }
            Mobile.validateRoutingData(pending.absolutePath)
            Mobile.validateRoutingPolicy(json(policy).toString(), pending.absolutePath)
            File(pending, "manifest.json").writeText(JSONObject().put("updated_at", System.currentTimeMillis() / 1000).put("datasets", datasets).toString())
            val pointer = AtomicFile(File(root, "active-snapshot"))
            val output = pointer.startWrite()
            try { output.write(pending.name.toByteArray(Charsets.UTF_8)); pointer.finishWrite(output) }
            catch (e: Exception) { pointer.failWrite(output); throw e }
            published = true
            validated = pending
        } finally { if (!published) pending.deleteRecursively() }
    }

    fun json(policy: RoutingPolicy): JSONObject = JSONObject()
        .put("enabled", true).put("mode", "rules")
        .put("ru_sites_direct", policy.ruSitesDirect).put("ru_ips_direct", policy.ruIpsDirect)
        .put("block_ads_trackers", policy.blockAdsTrackers).put("block_malware", policy.blockMalware)
        .put("direct", JSONArray(NativeRule.canonicalList(policy.rules.Direct)))
        .put("proxy", JSONArray(NativeRule.canonicalList(policy.rules.VPN)))
        .put("block", JSONArray(NativeRule.canonicalList(policy.rules.Block)))

    fun validate(context: Context, policy: RoutingPolicy) {
        Mobile.validateRoutingPolicy(json(policy).toString(), prepare(context).absolutePath)
    }

    fun check(context: Context, policy: RoutingPolicy, target: String): String {
        val value = target.trim()
        val address = if (Addresses.ip(value) != null) value else ""
        val host = if (address.isEmpty()) Addresses.domain(value) else ""
        val result = JSONObject(Mobile.checkRouting(json(policy).toString(), prepare(context).absolutePath, host, address))
        return result.getString("Action") + " · " + result.optString("Reason", "default rule")
    }
}
