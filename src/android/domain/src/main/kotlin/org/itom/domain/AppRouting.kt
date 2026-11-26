package org.itom.domain

import java.util.Locale

enum class AppRouteChoice { AUTO, DIRECT, VPN }

/** Android UID-level exclusions are independent from native destination rules. */
object AppRouting {
    val alwaysCaptured = setOf("com.android.providers.downloads", "com.android.providers.downloads.ui")
    private val russianPrefixes = listOf("ru.", "com.yandex.", "com.tinkoff.", "com.sberbank.", "com.vkontakte.", "com.vk.", "com.wildberries.", "com.avito.", "com.rustore.")
    private val russianPackages = setOf("com.vkontakte.android", "com.vk.im", "ru.ok.android", "ru.oneme.app", "com.ozon.app.android", "com.wildberries.ru", "ru.rutube.app", "ru.sberbankmobile", "ru.vtb24.mobilebanking.android", "com.idamob.tinkoff.android", "ru.gosuslugi.gosuslugi")
    private val russianBrands = setOf("яндекс", "вконтакте", "сбербанк", "госуслуги", "т-банк", "rutube", "rustore", "wildberries", "ozon", "одноклассники")

    fun isRussian(app: InstalledApp): Boolean {
        val name = app.packageName.lowercase(Locale.ROOT)
        val label = app.label.trim().lowercase(Locale.ROOT)
        return name in russianPackages || russianPrefixes.any(name::startsWith) ||
            russianBrands.any { label == it || label.startsWith("$it:") || label.startsWith("$it ") }
    }

    fun choice(policy: AppPolicy, packageName: String): AppRouteChoice = when {
        packageName in policy.vpnPackages -> AppRouteChoice.VPN
        packageName in policy.directPackages -> AppRouteChoice.DIRECT
        policy.mode == AppMode.INCLUDE_SELECTED -> if (packageName in policy.packages) AppRouteChoice.VPN else AppRouteChoice.DIRECT
        policy.mode == AppMode.EXCLUDE_SELECTED && packageName in policy.packages -> AppRouteChoice.DIRECT
        else -> AppRouteChoice.AUTO
    }

    fun groupCaptured(group: List<InstalledApp>): Boolean =
        group.any { it.packageName in alwaysCaptured }

    fun exclusions(policy: AppPolicy, catalog: List<InstalledApp>): Set<String> =
        catalog.groupBy { it.uid }.values.flatMap { group ->
            if (groupCaptured(group) || group.any { choice(policy, it.packageName) == AppRouteChoice.VPN }) emptyList()
            else if (group.all { app ->
                val selected = choice(policy, app.packageName)
                selected == AppRouteChoice.DIRECT || (selected == AppRouteChoice.AUTO &&
                    policy.mode == AppMode.ALL && policy.autoRussian && isRussian(app))
            }) group.map { it.packageName }
            else emptyList()
        }.toSet()

    fun migrate(policy: AppPolicy, catalog: List<InstalledApp>): AppPolicy {
        if (policy.mode == AppMode.ALL) return policy
        val direct = exclusions(policy, catalog)
        val vpn = catalog.filter { choice(policy, it.packageName) == AppRouteChoice.VPN }.map { it.packageName }
        return AppPolicy(autoRussian = false, directPackages = direct.sorted(), vpnPackages = vpn.sorted())
    }

    fun select(policy: AppPolicy, app: InstalledApp, catalog: List<InstalledApp>, choice: AppRouteChoice): AppPolicy {
        val group = catalog.filter { it.uid == app.uid }
        val names = group.map { it.packageName }.toSet()
        demand(choice != AppRouteChoice.DIRECT || !groupCaptured(group), "APP_MUST_CAPTURE")
        val direct = policy.directPackages.toSet() - names
        val vpn = policy.vpnPackages.toSet() - names
        return policy.copy(mode = AppMode.ALL, packages = emptyList(),
            directPackages = (direct + if (choice == AppRouteChoice.DIRECT) names else emptySet()).sorted(),
            vpnPackages = (vpn + if (choice == AppRouteChoice.VPN) names else emptySet()).sorted())
    }

    fun validateStored(policy: AppPolicy) {
        val direct = policy.directPackages
        val vpn = policy.vpnPackages
        demand(direct.size + vpn.size <= 4096 && direct.distinct().size == direct.size &&
            vpn.distinct().size == vpn.size && direct.toSet().intersect(vpn.toSet()).isEmpty(), "INVALID_APP_LIST")
        demand((direct + vpn).all { it.matches(Regex("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z0-9_]+)*")) }, "INVALID_PACKAGE")
        demand(policy.mode == AppMode.ALL || direct.isEmpty() && vpn.isEmpty(), "INVALID_APP_LIST")
        demand(direct.none { it in alwaysCaptured }, "APP_MUST_CAPTURE")
    }

    fun validate(policy: AppPolicy, catalog: List<InstalledApp>) {
        validateStored(policy)
        if (policy.mode != AppMode.ALL) return
        val explicit = (policy.directPackages + policy.vpnPackages).toSet()
        demand(explicit.all { name -> catalog.any { it.packageName == name } }, "APP_NOT_INSTALLED")
        catalog.groupBy { it.uid }.values.forEach { group ->
            val choices = group.map { choice(policy, it.packageName) }.distinct()
            demand(choices.size == 1, "SHARED_UID_PARTIAL_SELECTION")
            demand(choices.single() != AppRouteChoice.DIRECT || !groupCaptured(group), "APP_MUST_CAPTURE")
        }
    }
}
