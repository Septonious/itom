package org.itom.domain

import kotlin.test.*

class NetworkPolicyTest {
    @Test fun bothNetworksPreserveExistingBehavior() {
        for (kind in NetworkKind.entries) assertTrue(NetworkPolicy().allows(kind))
        assertTrue(NetworkPolicy().allows(null))
        val old = Codec.settings("""{"schema_version":5}""")
        assertEquals(NetworkPolicy(), old.networks)
    }
    @Test fun wifiOnlyStopsOnMobileButNotTemporaryLoss() {
        val policy = NetworkPolicy(wifi = true, mobile = false)
        assertTrue(policy.allows(NetworkKind.WIFI))
        assertFalse(policy.allows(NetworkKind.MOBILE))
        assertFalse(policy.allows(NetworkKind.OTHER))
        assertTrue(policy.allows(null))
    }
    @Test fun mobileOnlyStopsOnWifi() {
        val policy = NetworkPolicy(wifi = false, mobile = true)
        assertTrue(policy.allows(NetworkKind.MOBILE))
        assertFalse(policy.allows(NetworkKind.WIFI))
        assertTrue(policy.allows(null))
    }
    @Test fun selectionSurvivesVaultRoundTripAndCannotBeEmpty() {
        val original = VaultDocument(settings = AppSettings(networks = NetworkPolicy(false, true)))
        assertEquals(original.settings.networks, Codec.vault(Codec.encode(original)).settings.networks)
        val error = assertFailsWith<DomainError> { Validation.settings(AppSettings(networks = NetworkPolicy(false, false))) }
        assertEquals("NETWORK_SELECTION_EMPTY", error.code)
    }
    @Test fun validatedDefaultIsUsedEvenWhenOtherNetworkRemainsConnected() {
        val wifi = NetworkCandidate(1, NetworkKind.WIFI, true)
        val mobile = NetworkCandidate(2, NetworkKind.MOBILE, true)
        assertEquals(mobile, selectUnderlyingNetwork(listOf(wifi, mobile), 2, 1))
        assertEquals(wifi, selectUnderlyingNetwork(listOf(wifi, mobile), 1, 2))
        assertFalse(NetworkPolicy(false, true).allows(selectUnderlyingNetwork(listOf(wifi, mobile), 1, 2)?.kind))
    }
    @Test fun vpnDefaultCannotPinPreviousMobileOverValidatedWifi() {
        val wifi = NetworkCandidate(1, NetworkKind.WIFI, true)
        val mobile = NetworkCandidate(2, NetworkKind.MOBILE, true)
        assertEquals(wifi, selectUnderlyingNetwork(listOf(wifi, mobile), 99, 2))
    }
    @Test fun captiveWifiDoesNotDisplaceValidatedMobile() {
        val wifi = NetworkCandidate(1, NetworkKind.WIFI, false)
        val mobile = NetworkCandidate(2, NetworkKind.MOBILE, true)
        assertEquals(mobile, selectUnderlyingNetwork(listOf(wifi, mobile), 1, 1))
    }
    @Test fun disappearingNetworkDoesNotTriggerTypeBasedStop() {
        val chosen = selectUnderlyingNetwork(emptyList(), 1, 1)
        assertNull(chosen)
        assertTrue(NetworkPolicy(true, false).allows(chosen?.kind))
    }
}
