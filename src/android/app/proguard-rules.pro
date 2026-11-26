# Kotlin serialization generates its serializers; no blanket keep rule is needed.
-keepattributes RuntimeVisibleAnnotations,AnnotationDefault

-keep class go.** { *; }
-keep class org.itom.nativecore.** { *; }
