plugins {
    id("com.android.application")
}

android {
    namespace = "dev.testbuddy.bench"
    compileSdk = 36

    defaultConfig {
        applicationId = "dev.testbuddy.bench"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "1.0"
    }

    buildTypes {
        debug { isMinifyEnabled = false }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
