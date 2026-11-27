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
import org.itom.client.NativeRouting
import org.itom.nativecore.mobile.Client
import org.itom.nativecore.mobile.Host
import org.itom.nativecore.mobile.Mobile
import org.json.JSONObject
import java.io.File
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicReference
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

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
        backend.recordStartup(expected, "{}", StartupOutcome.STOPPED)
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
        var exitLookup: Job? = null
        var exitRequested = false
        val stoppedByNetworkPolicy = AtomicBoolean(false)
        val restartForNetwork = AtomicBoolean(false)
        val lastSoftRebindAt = AtomicLong(0L)
        val softRebindAttempts = AtomicLong(0L)
        val attachedNetworkHandle = AtomicLong(0L)
        val underlyingWasLost = AtomicBoolean(false)
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
            // Seed only the default transport synchronously. registerNetworkCallback
            // below reports every matching existing and future non-VPN network;
            // enumerating allNetworks is deprecated and races VPN establishment.
            manager.activeNetwork?.let { network ->
                manager.getNetworkCapabilities(network)?.let { capabilities[network] = it }
                manager.getLinkProperties(network)?.let { links[network] = it }
                remember(network)
            }
            val selectedUnderlying = AtomicReference<Network?>(null)
            val networkSelectionLock = Any()
            fun kindOf(caps: NetworkCapabilities?): NetworkKind? = caps?.let {
                when {
                    it.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> NetworkKind.WIFI
                    it.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> NetworkKind.MOBILE
                    else -> NetworkKind.OTHER
                }
            }
            fun selectedNetwork(): Network? = synchronized(networkSelectionLock) {
                if (stoppedByNetworkPolicy.get() || backend.snapshot.value.generation != g) return@synchronized null
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
                    return@synchronized null
                }
                if (selectedUnderlying.getAndSet(chosen) != chosen) {
                    setUnderlyingNetworks(chosen?.let { arrayOf(it) } ?: emptyArray())
                    networkEvent("selected_${kind?.name?.lowercase() ?: "none"}", chosen)
                }
                val attached = attachedNetworkHandle.get()
                if (attached > 0 && chosen == null) underlyingWasLost.set(true)
                if (requiresTransportRebind(attached, chosen?.networkHandle, underlyingWasLost.get()) &&
                    attachedNetworkHandle.compareAndSet(attached, chosen!!.networkHandle)) {
                    underlyingWasLost.set(false)
                    networkEvent("transport_soft_rebind_${kind?.name?.lowercase() ?: "other"}", chosen)
                    // Keep the TUN and foreground service, replace the provider generation.
                    // Existing tunneled TCP sessions may need to reconnect.
                    traceClient.get()?.rebindNetwork()
                    lastSoftRebindAt.set(SystemClock.elapsedRealtime())
                    softRebindAttempts.incrementAndGet()
                }
                chosen
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
            val routingData = NativeRouting.prepare(this@ItomVpnService)
            val runtimeJson = JSONObject(config.json).put("RoutingPolicy", NativeRouting.json(config.settings.routing))
                .put("RoutingDataDir", routingData.absolutePath).toString()
            val host = object : Host {
                override fun startup(snapshot: String): Boolean = callbacks.call("UNDERLYING_NETWORK_FAILED", false) {
                    if (!sessionJob.isActive) return@call false
                    val now = SystemClock.elapsedRealtime()
                    backend.machine.sample(g, Metrics(totalLines = config.profile.totalLines), snapshot, now)
                    if (diagnosticsEnabled && backend.snapshot.value.generation == g) backend.diagnosticState.value = snapshot
                    backend.recordStartup(g, snapshot)
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
                // Native policy is authoritative. A missing policy must fail closed.
                override fun route(host: String): String = "BLOCK"
            }
            val directory = File(noBackupFilesDir, "transport/$profileId").apply { mkdirs() }
            native = Mobile.new_(runtimeJson, directory.absolutePath, host)
            val core = requireNotNull(native)
            traceClient.set(core)
            backend.adminClient = core
            val initialNetwork = selectedNetwork()
            networkEvent("initial_${kindOf(initialNetwork?.let { capabilities[it] })?.name?.lowercase() ?: "none"}", initialNetwork)
            currentCoroutineContext().ensureActive()
            suspend fun establishTun(apps: AppPolicy, replace: Boolean) {
                val catalog = (application as ItomApplication).graph.catalog.load()
                val builder = Builder().setSession("ITOM").setMtu(1500)
                    .addAddress("172.19.0.1", 30).addDnsServer("172.19.0.2").addRoute("0.0.0.0", 0)
                    .addAddress("fd19:6974:6f6d::1", 128).addRoute("::", 0)
                    .setBlocking(false)
                builder.addDisallowedApplication(packageName)
                AppRouting.exclusions(apps, catalog).filter { it != packageName }.sorted().forEach { builder.addDisallowedApplication(it) }
                currentCoroutineContext().ensureActive()
                demand(backend.snapshot.value.generation == g, "CONFIG_CHANGED")
                val tun = builder.establish() ?: throw DomainError("VPN_PERMISSION_REVOKED")
                try {
                    val fd = tun.detachFd().toLong()
                    if (replace) core.replaceTun(fd) else core.attach(fd)
                } finally { tun.close() }
            }
            var appliedApps = config.settings.appRouting
            establishTun(appliedApps, false)
            // Only now is the native transport actually attached to this network.
            // A switch during startup is handled by the next callback or heartbeat.
            attachedNetworkHandle.set(initialNetwork?.networkHandle ?: 0L)
            selectedNetwork()
            val started = SystemClock.elapsedRealtime()
            var previous = started
            var previousDown = 0L; var previousUp = 0L
            var lastHealthy = started
            var lastTransportHealthCheck = started
            var previousAcked = 0L
            var previousPayloadBytes = 0L
            var lastDemandAt = started
            while (currentCoroutineContext().isActive && backend.snapshot.value.generation == g) {
                val now = SystemClock.elapsedRealtime()
                val latestApps = backend.vault.state.value.settings.appRouting
                if (latestApps != appliedApps) {
                    establishTun(latestApps, true)
                    appliedApps = latestApps
                    networkEvent("app_routing_tun_replaced", selectedUnderlying.get())
                }
                // The physical network, not the VPN network, decides whether
                // severe-mobile account confinement is eligible.
                val physical = selectedUnderlying.get()
                core.setNetworkType(when (kindOf(physical?.let { capabilities[it] })) { NetworkKind.WIFI -> "wifi"; NetworkKind.MOBILE -> "cellular"; else -> "unknown" })
                val snapshot = core.snapshot()
                backend.adminState.value = core.adminSnapshot()
                if (diagnosticsEnabled && backend.snapshot.value.generation == g) backend.diagnosticState.value = core.diagnostics()
                val stats = JSONObject(snapshot)
                val underlyingAvailable = selectedUnderlying.get() != null
                // A long interval with no Wi-Fi or mobile network is not a
                // terminal VPN failure. Preserve the foreground session and
                // its network callbacks until a usable network returns.
                val rebinding = stats.optBoolean("NetworkRebinding", false)
                if (underlyingAvailable && !rebinding) demand(stats.optString("Error").isEmpty(), "TRANSPORT_FAILED")
                val active = if (underlyingAvailable) stats.getInt("ActiveLines") else 0
                if (active > 0) {
                    lastHealthy = now
                    if (backend.machine.connected(g, now, interfaceUp = true)) backend.recordStartup(g, snapshot, StartupOutcome.CONNECTED)
                } else if (underlyingAvailable && now - lastHealthy > 90_000 && softRebindAttempts.get() >= 2) {
                    // Last-resort repair only. Normal network changes must not
                    // destroy the session or reset VPN application sockets.
                    networkEvent("transport_last_resort_restart", selectedUnderlying.get())
                    if (restartForNetwork.compareAndSet(false, true)) sessionJob.cancel()
                }
                // One lookup per connected bearer. Never read the phone's direct IP.
                if (rebinding || !underlyingAvailable) {
                    exitLookup?.cancel(); exitLookup = null; exitRequested = false
                    backend.machine.exitIdentity(g, profileId, null)
                } else if (active > 0 && backend.snapshot.value.phase == Phase.CONNECTED && !exitRequested) {
                    exitRequested = true
                    exitLookup = scope.launch {
                        val identity = try {
                            val data = JSONObject(core.exitIdentity())
                            ExitIdentity(data.getString("ip"), data.optString("country"))
                        } catch (e: CancellationException) { throw e } catch (_: Exception) { null }
                        currentCoroutineContext().ensureActive()
                        backend.machine.exitIdentity(g, profileId, identity)
                    }
                }
                val down = stats.getLong("Download"); val up = stats.getLong("Upload")
                if (down > previousDown || up > previousUp) lastDemandAt = now
                val elapsed = (now - previous).coerceAtLeast(1)
                backend.machine.sample(g, Metrics((down - previousDown).coerceAtLeast(0) * 8000 / elapsed,
                    (up - previousUp).coerceAtLeast(0) * 8000 / elapsed,
                    stats.optInt("RTTMillis").takeIf { active > 0 && it > 0 },
                    stats.getInt("TCP"), active, stats.getInt("TotalLines")), stats.toString(), now)
                previous = now; previousDown = down; previousUp = up
                if (!rebinding && now - lastTransportHealthCheck >= 5000 && !restartForNetwork.get()) {
                    lastTransportHealthCheck = now
                    // DEV7: authenticated application acknowledgements and DATA
                    // delivery determine progress. Heartbeats and ready sockets
                    // cannot conceal a blocked mailbox transport.
                    val diagnostic = JSONObject(core.diagnostics())
                    val health = diagnostic.optJSONObject("Session")
                    val mail = diagnostic.optJSONObject("Mail")
                    if (health != null) {
                        val acked = health.optLong("app_acked_packets", 0)
                        val payload = health.optLong("app_payload_bytes", 0)
                        if (acked > previousAcked || payload > previousPayloadBytes) softRebindAttempts.set(0L)
                        previousAcked = acked
                        previousPayloadBytes = payload
                        val lines = mail?.optJSONArray("lines")
                        var outstanding = 0
                        if (lines != null) for (i in 0 until lines.length()) {
                            outstanding += lines.optJSONObject(i)?.optInt("control_outstanding", 0)?.coerceAtLeast(0) ?: 0
                        }
                        val progress = TransportProgress(
                            underlyingAvailable = selectedUnderlying.get() != null,
                            streams = health.optInt("streams", 0),
                            recentDemand = now - lastDemandAt <= 30_000,
                            appAckAgeMs = health.optLong("last_app_ack_age_ms", 0),
                            appPayloadAgeMs = health.optLong("last_app_payload_age_ms", 0),
                            oldestPendingAgeMs = health.optLong("oldest_pending_age_ms", 0),
                            pendingPackets = health.optInt("pending_packets", 0),
                            mailboxOutstanding = outstanding,
                            selectiveAttempts = softRebindAttempts.get()
                        )
                        when (transportRecoveryStep(progress)) {
                            RecoveryStep.NONE -> Unit
                            RecoveryStep.RESTART -> if (now - lastSoftRebindAt.get() >= 20_000) {
                                networkEvent("transport_no_delivery_last_resort", selectedUnderlying.get())
                                if (restartForNetwork.compareAndSet(false, true)) sessionJob.cancel()
                            }
                            RecoveryStep.ACCOUNT -> if (now - lastSoftRebindAt.get() >= 15_000) {
                                core.rebindNetwork()
                                networkEvent("transport_no_delivery_account_rebind", selectedUnderlying.get())
                                lastSoftRebindAt.set(now)
                                softRebindAttempts.incrementAndGet()
                            }
                        }
                    }
                }
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
                exitLookup?.cancel()
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
                val last = TransportDiagnostics.parse(backend.diagnosticState.value).root.objectField("LastStartup")?.toString() ?: "{}"
                backend.recordStartup(g, last, if (failure == null) StartupOutcome.STOPPED else StartupOutcome.FAILED, failure)
                runCatching { networkEvent("session_stop", null) }
                traceClient.set(null)
                if (backend.snapshot.value.generation == g) { backend.adminClient = null; backend.adminState.value = "{}" }
                try { native?.close() } catch (_: Exception) { failure = failure ?: "NATIVE_CLOSE_FAILED" }
                exitLookup?.join()
                if (backend.snapshot.value.generation == g) {
                    // Never restart a user-stopped generation. A handover must
                    // first fully close its sockets and TUN, then reopen cleanly.
                    val restarting = restartForNetwork.get() && !stoppedByNetworkPolicy.get() &&
                        backend.restartAfterHandover(g, profileId, backend.vault.state.value.revision, backend.snapshot.value.metrics.totalLines)
                    if (!restarting) {
                        if (failure == null || stoppedByNetworkPolicy.get()) backend.machine.stop(g)
                        else backend.machine.fail(g, failure!!)
                        withContext(Dispatchers.Main) { stopForeground(STOP_FOREGROUND_REMOVE); stopSelf() }
                    }
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
