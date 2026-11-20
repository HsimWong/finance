# 家庭记账 APK 构建

把手机端页面包成一个原生 WebView 壳，解决小米手机 PWA「添加到主屏幕」没有图标的问题。

## 关键设计

- 启动地址：`https://47.94.236.198/finance/`（阿里云固定入口，Nginx 剥 `/finance/` 前缀反代到后端）。
  用固定 IP 而不是 cloudflared 隧道域名，隧道域名会随机变；证书是 Let's Encrypt 给该 IP 签发的，SAN 含该 IP，Android 信任。
- 应用名「家庭记账」，桌面图标复用 PWA 的紫色账本 ¥ 图标（mdpi~xxxhdpi 五档）。
- 权限仅 INTERNET + CAMERA；拍照记账走 `onShowFileChooser`，选择图片后交给页面 OCR。
- 中文应用名依赖 strings.xml；项目必须放在**纯 ASCII 路径**构建（`C:/Users/WangXin/WorkBuddy/finance-apk-build`），
  否则 AGP 报 "project path contains non-ASCII characters" 直接失败。

## 工具链（本机已装，不入库）

- JDK 17：`C:\Program Files\Eclipse Adoptium\jdk-17.0.21.9-hotspot`
- Android SDK：`.apkbuild/sdk`（platform android-34 + build-tools 34.0.0）
- Gradle 8.9：`.apkbuild/gradle-8.9`
- 签名密钥：`.apkbuild/family-finance.jks`（**入库保留**，换签名手机会拒绝覆盖安装）

## 重新打包步骤

1. 改 `app/src/main/java/com/familyfinance/ledger/MainActivity.java` 里的 `START_URL`。
2. 把 `.apkbuild/` 下的 `settings.gradle`、`build.gradle`、`gradle.properties`、`app/`、`family-finance.jks`
   同步到 `C:/Users/WangXin/WorkBuddy/finance-apk-build/`（AGP 要求纯 ASCII 路径）。
3. PowerShell 构建（Bash 里 gradle.bat 读不到 JAVA_HOME）：
   设置 `$env:JAVA_HOME` 为上面的 JDK 路径、`$env:ANDROID_HOME` 为 `.apkbuild/sdk` 绝对路径，
   `Set-Location` 到 `C:/Users/WangXin/WorkBuddy/finance-apk-build`，
   执行 `& "C:/Users/WangXin/WorkBuddy/财务管理/.apkbuild/gradle-8.9/bin/gradle.bat" assembleRelease --no-daemon`。
   注：PowerShell 会把 javac 的 deprecation 警告当成 NativeCommandError，只要输出含 `BUILD SUCCESSFUL` 就是成功。
4. 产物 `finance-apk-build/app/build/outputs/apk/release/app-release.apk` 拷到 `dist/家庭记账-1.0.apk`。
5. 验签：`.apkbuild/sdk/build-tools/34.0.0/apksigner.bat verify --print-certs <apk>`（须在 PowerShell 里设 JAVA_HOME）。
