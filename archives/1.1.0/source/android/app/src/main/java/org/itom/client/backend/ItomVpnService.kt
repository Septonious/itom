package org.itom.client.backend

import android.app.*
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Build
import android.os.SystemClock
import android.os.ParcelFileDescriptor
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.itom.client.*
import org.itom.domain.*
import org.itom.nativecore.mobile.Client
import org.itom.nativecore.mobile.Host
import org.itom.nativecore.mobile.Mobile
import org.json.JSONObject
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicReference
import java.util.concurrent.atomic.AtomicBoolean

class ItomVpnService : VpnService() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val lifecycle = Mutex()
    private var running: Job? = null
    private var generation = 0L
    private val backend get() = ((application as ItomApplication).graph.backend as NativeBackend)
    override fun onCreate() {
        super.onCreate(); backend.service = this
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel("itom_vpn", "ITOM VPN", NotificationManager.IMPORTANCE_LOW))
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val requested = intent?.getLongExtra("generation", -1) ?: -1
        if (intent?.action == "STOP") { requestStop(requested); return START_NOT_STICKY }
        if (requested != backend.snapshot.value.generation || backend.snapshot.value.phase != Phase.CONNECTING) {
            if (running == null) stopSelf(startId)
            return START_NOT_STICKY
        }
        val command = intent ?: return START_NOT_STICKY
        generation = requested
        running?.cancel()
        try {
            val notification = notification()
            if (Build.VERSION.SDK_INT >= 34) startForeground(72, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
            else startForeground(72, notification)
        } catch (_: RuntimeException) {
            backend.machine.fail(requested, "VPN_SERVICE_START_FAILED")
            stopSelf(startId)
            return START_NOT_STICKY
        }
        val profile = command.getStringExtra("profile") ?: return START_NOT_STICKY
        val revision = command.getLongExtra("revision", -1)
        running = scope.launch { lifecycle.withLock { runSession(requested, profile, revision) } }
        return START_NOT_STICKY
    }
    fun requestStop(expected: Long) {
        if (backend.snapshot.value.generation != expected) return
        backend.machine.stop(expected)
        running?.cancel()
        scope.launch { lifecycle.withLock { }
            if (backend.snapshot.value.phase == Phase.OFF) withContext(Dispatchers.Main) { stopForeground(STOP_FOREGROUND_REMOVE); stopSelf() }
        }
    }
    private suspend fun runSession(g: Long, profileId: String, revision: Long) {
        var native: Client? = null
        var failure: String? = null
        var connectivity: ConnectivityManager? = null
        var networkCallback: ConnectivityManager.NetworkCallback? = null
        var defaultNetworkCallback: ConnectivityManager.NetworkCallback? = null
        var policyWatcher: Job? = null
        val stoppedByNetworkPolicy = AtomicBoolean(false)
        val sessionJob = currentCoroutineContext().job
        val callbacks = SessionCallbacks(
            active = { backend.snapshot.value.generation == g },
            onFailure = { sessionJob.cancel() },
        )
        var diagnosticsEnabled = false
        val traceClient = AtomicReference<Client?>(null)
        fun networkEvent(kind: String, network: Network?) {
            traceClient.get()?.recordNetworkEvent(kind, network?.networkHandle ?: 0L)
        }
        try {
            currentCoroutineContext().ensureActive()
            if (backend.snapshot.value.generation != g) return
            val manager = getSystemService(ConnectivityManager::class.java)
            connectivity = manager
            val underlying = ConcurrentHashMap<Network, List<String>>()
            val capabilities = ConcurrentHashMap<Network, NetworkCapabilities>()
            val links = ConcurrentHashMap<Network, LinkProperties>()
            fun remember(network: Network) {
                val caps = capabilities[network]
                if (caps == null || caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) ||
                    !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) {
                    underlying.remove(network)
                    return
                }
                val servers = links[network]?.dnsServers?.mapNotNull { it.hostAddress }?.filter { it.isNotBlank() }.orEmpty()
                if (servers.isEmpty()) underlying.remove(network) else underlying[network] = servers
            }
            val defaultUnderlying = AtomicReference<Network?>(manager.activeNetwork)
            manager.allNetworks.forEach { network ->
                manager.getNetworkCapabilities(network)?.let { capabilities[network] = it }
                manager.getLinkProperties(network)?.let { links[network] = it }
                remember(network)
            }
            val selectedUnderlying = AtomicReference<Network?>(null)
            fun kindOf(caps: NetworkCapabilities?): NetworkKind? = caps?.let {
                when {
                    it.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> NetworkKind.WIFI
                    it.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> NetworkKind.MOBILE
                    else -> NetworkKind.OTHER
                }
            }
            fun selectedNetwork(): Network? {
                if (stoppedByNetworkPolicy.get() || backend.snapshot.value.generation != g) return null
                val networks = underlying.keys.toList()
                val candidates = networks.mapNotNull { network ->
                    capabilities[network]?.let { caps -> NetworkCandidate(network.networkHandle,
                        requireNotNull(kindOf(caps)), caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) }
                }
                val choice = selectUnderlyingNetwork(candidates, defaultUnderlying.get()?.networkHandle, selectedUnderlying.get()?.networkHandle)
                val chosen = networks.firstOrNull { it.networkHandle == choice?.handle }
                val kind = choice?.kind
                if (!backend.vault.state.value.settings.networks.allows(kind)) {
                    if (stoppedByNetworkPolicy.compareAndSet(false, true)) {
                        networkEvent("auto_stop_${kind?.name?.lowercase() ?: "none"}", chosen)
                        requestStop(g)
                    }
                    return null
                }
                if (selectedUnderlying.getAndSet(chosen) != chosen) {
                    setUnderlyingNetworks(chosen?.let { arrayOf(it) } ?: emptyArray())
                    networkEvent("selected_${kind?.name?.lowercase() ?: "none"}", chosen)
                }
                return chosen
            }
            val callback = object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { networkEvent("available", network) } }
                override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { networkEvent("capabilities", network); capabilities[network] = caps; remember(network); selectedNetwork(); Unit } }
                override fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { networkEvent("link_properties", network); links[network] = properties; remember(network); selectedNetwork(); Unit } }
                override fun onLost(network: Network) { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { networkEvent("lost", network); underlying.remove(network); capabilities.remove(network); links.remove(network); selectedNetwork(); Unit } }
            }
            val request = NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                .build()
            manager.registerNetworkCallback(request, callback)
            networkCallback = callback
            val defaults = object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { defaultUnderlying.set(network) } }
                override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                    callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { if (!caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) {
                        capabilities[network] = caps
                        defaultUnderlying.set(network)
                        remember(network)
                        selectedNetwork()
                    }; Unit }
                }
                override fun onLost(network: Network) { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { defaultUnderlying.compareAndSet(network, null); selectedNetwork(); Unit } }
            }
            manager.registerDefaultNetworkCallback(defaults)
            defaultNetworkCallback = defaults
            policyWatcher = CoroutineScope(currentCoroutineContext()).launch {
                backend.vault.state.map { it.settings.networks }.distinctUntilChanged().collect { callbacks.call("UNDERLYING_NETWORK_FAILED", Unit) { selectedNetwork(); Unit } }
            }
            fun underlyingDNS(): List<String> {
                return selectedNetwork()?.let { underlying[it] } ?: emptyList()
            }
            val dns = underlyingDNS()
            val config = backend.vault.runtime(profileId, revision, dns)
            diagnosticsEnabled = config.settings.diagnosticsEnabled
            if (!diagnosticsEnabled && backend.snapshot.value.generation == g) backend.diagnosticState.value = "{}"
            val router = Router(config.settings.routing)
            val host = object : Host {
                override fun startup(snapshot: String): Boolean = callbacks.call("UNDERLYING_NETWORK_FAILED", false) {
                    if (!sessionJob.isActive) return@call false
                    val now = SystemClock.elapsedRealtime()
                    backend.machine.sample(g, Metrics(totalLines = config.profile.totalLines), snapshot, now)
                    if (diagnosticsEnabled && backend.snapshot.value.generation == g) backend.diagnosticState.value = snapshot
                    true
                }
                override fun dns(): String = callbacks.call("UNDERLYING_NETWORK_FAILED", "[]") { org.json.JSONArray(underlyingDNS()).toString() }
                override fun lookup(host: String): String {
                    return try {
                        val network = selectedNetwork() ?: return "[]"
                        org.json.JSONArray(network.getAllByName(host).mapNotNull { it.hostAddress }).toString()
                    } catch (_: Exception) { "[]" }
                }
                override fun protect(fd: Long): Boolean {
                    return try {
                        val network = selectedNetwork() ?: return false
                        if (!this@ItomVpnService.protect(fd.toInt())) return false
                        // fromFd duplicates the descriptor; closing the wrapper leaves
                        // Go's original socket alive. Bind before connect in Control.
                        ParcelFileDescriptor.fromFd(fd.toInt()).use { network.bindSocket(it.fileDescriptor) }
                        true
                    } catch (_: Exception) { false }
                }
                override fun route(host: String): String = try {
                    val decision = if (Addresses.ip(host) != null) router.decide(address = host) else router.decide(host = host)
                    decision.action.name
                } catch (_: Exception) { "BLOCK" }
            }
            val directory = File(noBackupFilesDir, "transport/$profileId").apply { mkdirs() }
            native = Mobile.new_(config.json, directory.absolutePath, host)
            val core = requireNotNull(native)
            traceClient.set(core)
            backend.adminClient = core
            val initialNetwork = selectedNetwork()
            networkEvent("initial_${kindOf(initialNetwork?.let { capabilities[it] })?.name?.lowercase() ?: "none"}", initialNetwork)
            currentCoroutineContext().ensureActive()
            val builder = Builder().setSession("ITOM").setMtu(1500)
                .addAddress("172.19.0.1", 30).addDnsServer("172.19.0.2").addRoute("0.0.0.0", 0)
                .addAddress("fd19:6974:6f6d::1", 128).addRoute("::", 0)
                .setBlocking(false)
            val apps = config.settings.appRouting
            when (apps.mode) {
                AppMode.INCLUDE_SELECTED -> {
                    demand(apps.packages.isNotEmpty(), "EMPTY_INCLUDE")
                    apps.packages.forEach { builder.addAllowedApplication(it) }
                }
                AppMode.EXCLUDE_SELECTED -> {
                    builder.addDisallowedApplication(packageName)
                    apps.packages.filter { it != packageName }.forEach { builder.addDisallowedApplication(it) }
                }
                AppMode.ALL -> builder.addDisallowedApplication(packageName)
            }
            // Establish immediately: while reconnecting, captured traffic cannot escape.
            val tun = builder.establish() ?: throw DomainError("VPN_PERMISSION_REVOKED")
            try { core.attach(tun.detachFd().toLong()) } finally { tun.close() }
            val started = SystemClock.elapsedRealtime()
            var previous = started
            var previousDown = 0L; var previousUp = 0L
            val accountTotals = mutableMapOf<String, Pair<Long, Long>>()
            var lastHealthy = started
            while (currentCoroutineContext().isActive && backend.snapshot.value.generation == g) {
                val now = SystemClock.elapsedRealtime()
                val snapshot = core.snapshot()
                backend.adminState.value = core.adminSnapshot()
                val stats = JSONObject(snapshot)
                demand(stats.optString("Error").isEmpty(), "TRANSPORT_FAILED")
                val active = stats.getInt("ActiveLines")
                if (active > 0) {
                    lastHealthy = now
                    backend.machine.connected(g, now, interfaceUp = true)
                } else if (now - lastHealthy > 90_000) throw DomainError("TRANSPORT_TIMEOUT")
                val down = stats.getLong("Download"); val up = stats.getLong("Upload")
                val elapsed = (now - previous).coerceAtLeast(1)
                stats.optJSONArray("Lines")?.let { lines ->
                    for (index in 0 until lines.length()) {
                        val line = lines.getJSONObject(index)
                        val id = line.getString("Account")
                        val upload = line.optLong("UploadBytes")
                        val download = line.optLong("DownloadBytes")
                        val old = accountTotals[id]
                        line.put("UpBps", if (old == null) 0 else (upload - old.first).coerceAtLeast(0) * 8000 / elapsed)
                        line.put("DownBps", if (old == null) 0 else (download - old.second).coerceAtLeast(0) * 8000 / elapsed)
                        accountTotals[id] = upload to download
                    }
                }
                backend.machine.sample(g, Metrics((down - previousDown).coerceAtLeast(0) * 8000 / elapsed,
                    (up - previousUp).coerceAtLeast(0) * 8000 / elapsed, stats.optInt("RTTMillis").takeIf { it > 0 },
                    stats.getInt("TCP"), active, stats.getInt("TotalLines")), stats.toString(), now)
                previous = now; previousDown = down; previousUp = up
                if (Build.VERSION.SDK_INT < 33 || checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) == android.content.pm.PackageManager.PERMISSION_GRANTED) {
                    getSystemService(NotificationManager::class.java).notify(72, notification())
                }
                delay(1000)
            }
        } catch (e: CancellationException) { throw e }
        catch (e: DomainError) { failure = e.code }
        catch (e: Exception) { failure = nativeFailureCode(e.message) }
        finally {
            withContext(NonCancellable) {
                callbacks.close()
                failure = failure ?: callbacks.failure
                policyWatcher?.cancelAndJoin()
                defaultNetworkCallback?.let { callback -> runCatching { connectivity?.unregisterNetworkCallback(callback) } }
                networkCallback?.let { callback -> runCatching { connectivity?.unregisterNetworkCallback(callback) } }
                if (diagnosticsEnabled && backend.snapshot.value.generation == g) {
                    native?.let { client -> backend.diagnosticState.value = runCatching { client.diagnostics() }.getOrDefault(backend.diagnosticState.value) }
                    if (native == null && failure != null) {
                        backend.diagnosticState.value = JSONObject(backend.diagnosticState.value).put("StartupFailure", failure)
                            .put("SampledAtMS", System.currentTimeMillis()).toString()
                    }
                }
                runCatching { networkEvent("session_stop", null) }
                traceClient.set(null)
                if (backend.snapshot.value.generation == g) { backend.adminClient = null; backend.adminState.value = "{}" }
                try { native?.close() } catch (_: Exception) { failure = failure ?: "NATIVE_CLOSE_FAILED" }
                if (backend.snapshot.value.generation == g) {
                    if (failure == null || stoppedByNetworkPolicy.get()) backend.machine.stop(g) else backend.machine.fail(g, failure!!)
                    withContext(Dispatchers.Main) { stopForeground(STOP_FOREGROUND_REMOVE); stopSelf() }
                }
            }
        }
    }
    private fun notification(): Notification {
        val state = backend.snapshot.value
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(this, state.generation.toInt(), Intent(this, ItomVpnService::class.java)
            .setAction("STOP").putExtra("generation", state.generation), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val summary = Presentation.notification(state)
        return NotificationCompat.Builder(this, "itom_vpn").setSmallIcon(R.drawable.ic_itom)
            .setContentTitle(if (state.phase == Phase.CONNECTED) "ITOM · Connected" else "ITOM · Connecting")
            .setContentText(summary).setStyle(NotificationCompat.BigTextStyle().bigText(summary))
            .setContentIntent(open).setOnlyAlertOnce(true).setOngoing(true)
            .addAction(0, "Disconnect", stop).build()
    }
    override fun onRevoke() { requestStop(generation); super.onRevoke() }
    override fun onDestroy() {
        backend.service = null
        running?.cancel()
        // The coroutine joins native workers and closes TUN in its finally block.
        running?.invokeOnCompletion { scope.cancel() } ?: scope.cancel()
        super.onDestroy()
    }
}
