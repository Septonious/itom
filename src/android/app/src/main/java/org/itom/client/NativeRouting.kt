package org.itom.client

import android.content.Context
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
        val active = File(context.noBackupFilesDir, "routing-data/rc20")
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
            Mobile.validateRoutingData(pending.absolutePath)
            if (active.exists()) demand(active.deleteRecursively(), "ROUTING_DATA_STORAGE")
            demand(pending.renameTo(active), "ROUTING_DATA_STORAGE")
            validated = active
            return active
        } finally { if (pending.exists()) pending.deleteRecursively() }
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
