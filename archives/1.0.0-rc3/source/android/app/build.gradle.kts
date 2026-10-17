plugins { id("com.android.application"); kotlin("plugin.serialization"); id("org.jetbrains.kotlin.plugin.compose") }
android {
    namespace = "org.itom.client"
    compileSdk = 37
    compileSdkMinor = 0
    defaultConfig {
        applicationId = "org.itom.client"
        minSdk = 29; targetSdk = 37
        versionCode = 10002; versionName = "1.0.0-rc3"
        buildConfigField("String", "UI_BASELINE", "\"ui-rev12-startup\"")
        buildConfigField("int", "CONFIG_SCHEMA", "5")
    }
    buildTypes {
        debug { applicationIdSuffix = ".dev.rev7"; versionNameSuffix = "-debug"; resValue("string", "app_name", "ITOM") }
        release { isMinifyEnabled = true; resValue("string", "app_name", "ITOM"); proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro") }
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
