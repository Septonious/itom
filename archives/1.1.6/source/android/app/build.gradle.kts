plugins { id("com.android.application"); kotlin("plugin.serialization"); id("org.jetbrains.kotlin.plugin.compose") }
val itomSigningStore = providers.environmentVariable("ITOM_ANDROID_SIGNING_STORE_FILE").orNull
android {
    namespace = "org.itom.client"
    compileSdk = 37
    compileSdkMinor = 0
    defaultConfig {
        applicationId = "org.itom.client"
        minSdk = 29; targetSdk = 37
        versionCode = 10160; versionName = "1.1.6-rc1"
        buildConfigField("String", "UI_BASELINE", "\"ui-rev15-admin-grid\"")
        buildConfigField("int", "CONFIG_SCHEMA", "5")
    }
    signingConfigs {
        if (itomSigningStore != null) {
            create("itomField") {
                storeFile = file(itomSigningStore)
                storePassword = providers.environmentVariable("ITOM_ANDROID_STORE_PASSWORD").get()
                keyAlias = providers.environmentVariable("ITOM_ANDROID_KEY_ALIAS").get()
                keyPassword = providers.environmentVariable("ITOM_ANDROID_KEY_PASSWORD").get()
            }
        }
    }
    buildTypes {
        debug {
            // Independently installable field-test build. An ephemeral Android
            // debug signer must never overwrite or masquerade as stable 1.1.5.
            applicationIdSuffix = ".testing"
            versionNameSuffix = "-testing"
            resValue("string", "app_name", "ITOM RC1 Test")
            if (itomSigningStore != null) signingConfig = signingConfigs.getByName("itomField")
        }
        release { isDebuggable = false; isMinifyEnabled = false;
            if (itomSigningStore != null) signingConfig = signingConfigs.getByName("itomField")
            resValue("string", "app_name", "ITOM"); proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro") }
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    buildFeatures { compose = true; buildConfig = true; resValues = true }
    lint {
        abortOnError = true
        textReport = true
        textOutput = layout.buildDirectory.file("reports/lint-results.txt").get().asFile
        xmlReport = true
        xmlOutput = layout.buildDirectory.file("reports/lint-results.xml").get().asFile
    }
    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
        jniLibs.keepDebugSymbols += setOf("**/libgojni.so", "**/libandroidx.graphics.path.so")
    }
}
// AGP creates local tests for debug by default; qualify the shipped release too.
androidComponents {
    beforeVariants(selector().withBuildType("release")) { builder ->
        (builder as com.android.build.api.variant.HasUnitTestBuilder).enableUnitTest = true
    }
}
kotlin { compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) } }
dependencies {
    testImplementation(kotlin("test-junit"))
    testImplementation("junit:junit:4.13.2")
    implementation(files("libs/itom.aar"))
    implementation(project(":domain"))
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.11.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.11.0")
    implementation("androidx.core:core-ktx:1.19.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")
    implementation("com.google.zxing:core:3.5.4")
    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-tooling-preview")
}
