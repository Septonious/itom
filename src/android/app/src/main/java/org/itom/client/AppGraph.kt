package org.itom.client

import android.app.Application
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.os.Build
import android.graphics.Bitmap
import android.util.LruCache
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.core.graphics.drawable.toBitmap
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.StateFlow
import org.itom.client.backend.createBackend
import org.itom.domain.*

interface SessionBackend {
    val snapshot: StateFlow<SessionSnapshot>
    val diagnostics: StateFlow<String>
    val admin: StateFlow<String>
    suspend fun adminCall(request: String): String
    fun start(profile: ServerProfile, revision: Long)
    fun stop()
}

class PackageCatalog(private val context: Context) {
    // Bounded cache; icon decoding never runs on the UI thread or scans every icon at once.
    private val icons = object : LruCache<String, ImageBitmap>(4 * 1024) {
        override fun sizeOf(key: String, value: ImageBitmap) = (value.width * value.height * 4 / 1024).coerceAtLeast(1)
    }
    suspend fun load(): List<InstalledApp> = withContext(Dispatchers.IO) {
        icons.evictAll()
        val manager = context.packageManager
        val packages = if (Build.VERSION.SDK_INT >= 33) manager.getInstalledApplications(PackageManager.ApplicationInfoFlags.of(0))
        else @Suppress("DEPRECATION") manager.getInstalledApplications(0)
        // VpnService is per Android user/profile; this is that context's catalog, including shared UIDs.
        packages.filter { it.packageName != context.packageName }.map {
            InstalledApp(it.packageName, manager.getApplicationLabel(it).toString(), (it.flags and ApplicationInfo.FLAG_SYSTEM) != 0, it.uid)
        }.sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.label })
    }
    suspend fun icon(packageName: String): ImageBitmap? = withContext(Dispatchers.IO) {
        icons.get(packageName)?.let { return@withContext it }
        try {
            val size = (40 * context.resources.displayMetrics.density).toInt().coerceIn(40, 192)
            context.packageManager.getApplicationIcon(packageName).toBitmap(size, size, Bitmap.Config.ARGB_8888)
                .asImageBitmap().also { icons.put(packageName, it) }
        } catch (e: CancellationException) { throw e }
        catch (_: PackageManager.NameNotFoundException) { null }
        catch (_: RuntimeException) { null }
    }
}

class AppGraph(val context: Context) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    val vault = VaultStore(context)
    val catalog = PackageCatalog(context)
    val backend: SessionBackend = createBackend(context, vault)
    init { scope.launch { vault.load() } }
}
class ItomApplication : Application() {
    lateinit var graph: AppGraph; private set
    override fun onCreate() {
        super.onCreate()
        // Retire only the notification/channel owned by old UI prototypes after an in-place update.
        try {
            getSystemService(NotificationManager::class.java)?.apply { cancel(71); deleteNotificationChannel("itom_demo") }
        } catch (_: RuntimeException) { /* A notification cleanup failure must not prevent opening the vault. */ }
        graph = AppGraph(this)
    }
}
