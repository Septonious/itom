package org.itom.domain

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class AppRoutingReconstructionTest {
    private val ru = InstalledApp("ru.example.app", "Example", false, 10)
    private val international = InstalledApp("org.example.app", "Example", false, 11)
    private val sharedRu = InstalledApp("ru.shared.app", "Example", false, 12)
    private val sharedOther = InstalledApp("org.shared.app", "Example", false, 12)
    private val download = InstalledApp("com.android.providers.downloads", "Downloads", true, 13)
    private val sharedDownload = InstalledApp("ru.download.app", "Example", true, 13)
    private val catalog = listOf(ru, international, sharedRu, sharedOther, download, sharedDownload)
    @Test fun defaultPolicyDoesNotBypassMixedSharedUidsOrDownloads() {
        assertEquals(setOf(ru.packageName), AppRouting.exclusions(AppPolicy(), catalog))
        assertTrue(AppRouting.exclusions(AppPolicy(autoRussian = false), catalog).isEmpty())
    }
    @Test fun explicitOverridesAndSharedUidGroups() {
        var policy = AppRouting.select(AppPolicy(), ru, catalog, AppRouteChoice.VPN)
        policy = AppRouting.select(policy, international, catalog, AppRouteChoice.DIRECT)
        policy = AppRouting.select(policy, sharedRu, catalog, AppRouteChoice.DIRECT)
        AppRouting.validate(policy, catalog)
        assertEquals(setOf(international.packageName, sharedRu.packageName, sharedOther.packageName), AppRouting.exclusions(policy, catalog))
        policy = AppRouting.select(policy, sharedOther, catalog, AppRouteChoice.VPN)
        assertEquals(setOf(international.packageName), AppRouting.exclusions(policy, catalog))
        assertFailsWith<DomainError> { AppRouting.select(policy, sharedDownload, catalog, AppRouteChoice.DIRECT) }
    }
    @Test fun legacyExclusionMigrationDoesNotIntroduceAutomaticBypass() {
        val legacy = AppPolicy(AppMode.EXCLUDE_SELECTED, listOf(international.packageName))
        val migrated = AppRouting.migrate(legacy, catalog)
        assertEquals(AppMode.ALL, migrated.mode)
        assertEquals(AppRouting.exclusions(legacy, catalog), AppRouting.exclusions(migrated, catalog))
    }
    @Test fun nativeRuleSyntax() {
        assertEquals("domain:xn--e1afmkfd.xn--p1ai", NativeRule.canonical("пример.рф"))
        assertEquals("domain:ru", NativeRule.canonical(".ru"))
        assertEquals("ip:192.0.2.0/24", NativeRule.canonical("192.0.2.0/24"))
        assertEquals("full:login.example", NativeRule.canonical("full:LOGIN.EXAMPLE"))
        assertEquals("geosite:category-ads-all", NativeRule.canonical("geosite:category-ads-all"))
        assertFailsWith<DomainError> { NativeRule.canonical("geoip:us") }
    }
}
