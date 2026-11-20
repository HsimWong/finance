package com.familyfinance.ledger;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

/**
 * 家庭记账 · 原生壳。
 *
 * 直接加载阿里云固定入口的移动端页面 https://47.94.236.198/finance/mobile.html。
 * 不走入口根路径：根路径要靠后端 UA 判断 + desktop cookie 才跳移动版，
 * WebView 的 UA、残留 cookie 或登录后的相对回跳都可能让它落到桌面版。
 * 手机专用客户端应锁定移动版，故把所有指向桌面首页的导航拉回 mobile.html。
 *
 * 证书是 Let's Encrypt 给该 IP 签发的，SAN 含 47.94.236.198，Android 系统信任。
 * 为什么不用 PWA「添加到主屏幕」：小米/MIUI 上加的网页快捷方式经常没有图标、
 * 和普通书签混在一起。原生 APK 有真正的桌面图标与启动器条目。
 */
public class MainActivity extends Activity {
    private static final String BASE_URL = "https://47.94.236.198/finance/";
    private static final String MOBILE_URL = BASE_URL + "mobile.html";
    private static final int REQ_PICK_IMAGE = 1001;

    private WebView web;
    private ValueCallback<Uri[]> filePathCallback;

    /** 是否指向桌面首页（入口根路径）——需要拉回移动版 */
    private static boolean isDesktopLanding(String url) {
        if (url == null) return false;
        String clean = url.split("\\?")[0].split("#")[0];
        return clean.equals(BASE_URL) || clean.equals("https://47.94.236.198/finance");
    }

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        web = new WebView(this);
        setContentView(web);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);       // PWA / 页面本地状态
        s.setDatabaseEnabled(true);
        s.setLoadWithOverviewMode(true);
        s.setUseWideViewPort(true);
        s.setSupportZoom(false);
        s.setBuiltInZoomControls(false);
        s.setCacheMode(WebSettings.LOAD_DEFAULT);
        s.setUserAgentString(s.getUserAgentString() + " FamilyFinanceApp/1.0");

        // 页面跳转留在 WebView 内；桌面首页导航一律拉回移动版
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                String url = request.getUrl().toString();
                if (isDesktopLanding(url)) {
                    view.loadUrl(MOBILE_URL);
                    return true;
                }
                return false;
            }

            @Override
            public boolean shouldOverrideUrlLoading(WebView view, String url) {
                if (isDesktopLanding(url)) {
                    view.loadUrl(MOBILE_URL);
                    return true;
                }
                return false;
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                // 服务端 302 由 WebView 内部跟随，不触发 shouldOverrideUrlLoading；
                // 登录后 __auth 回跳到根路径时在这里兜底拉回移动版。
                if (isDesktopLanding(url)) view.loadUrl(MOBILE_URL);
            }
        });

        web.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> cb, FileChooserParams params) {
                if (filePathCallback != null) filePathCallback.onReceiveValue(null);
                filePathCallback = cb;
                Intent pick = new Intent(Intent.ACTION_GET_CONTENT);
                pick.addCategory(Intent.CATEGORY_OPENABLE);
                pick.setType("image/*");
                try {
                    startActivityForResult(Intent.createChooser(pick, "选择账单截图"), REQ_PICK_IMAGE);
                } catch (ActivityNotFoundException e) {
                    filePathCallback = null;
                    Toast.makeText(MainActivity.this, "没有可用的图片选择器", Toast.LENGTH_LONG).show();
                    return false;
                }
                return true;
            }
        });

        if (savedInstanceState == null) {
            web.loadUrl(MOBILE_URL);
        } else {
            web.restoreState(savedInstanceState);
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_PICK_IMAGE || filePathCallback == null) return;
        Uri[] results = null;
        if (resultCode == RESULT_OK && data != null && data.getData() != null) {
            results = new Uri[]{data.getData()};
        }
        filePathCallback.onReceiveValue(results);
        filePathCallback = null;
    }

    @Override
    public void onBackPressed() {
        if (web != null && web.canGoBack()) {
            web.goBack();
        } else {
            super.onBackPressed();
        }
    }

    @Override
    protected void onSaveInstanceState(Bundle outState) {
        super.onSaveInstanceState(outState);
        if (web != null) web.saveState(outState);
    }

    @Override
    protected void onDestroy() {
        if (web != null) web.destroy();
        super.onDestroy();
    }
}
