package com.familyfinance.ledger;

import android.app.Activity;
import android.app.ActivityManager;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

/**
 * 家庭记账 · 原生壳。
 *
 * 只做一件事：用一个全屏 WebView 打开阿里云固定入口 https://47.94.236.198/finance/。
 * 该地址由 Nginx 反代到本机服务，地址栏稳定不变，不依赖会随机变化的 cloudflared 隧道域名。
 * 证书是 Let's Encrypt 给该 IP 签发的，SAN 含 47.94.236.198，Android 系统信任。
 *
 * 为什么不用 PWA「添加到主屏幕」：小米/MIUI 上加的网页快捷方式经常没有图标、
 * 和普通书签混在一起。原生 APK 有真正的桌面图标与启动器条目。
 */
public class MainActivity extends Activity {
    private static final String START_URL = "https://47.94.236.198/finance/";
    private static final int REQ_PICK_IMAGE = 1001;

    private WebView web;
    private ValueCallback<Uri[]> filePathCallback;

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

        // 页面跳转留在 WebView 内，不调起系统浏览器
        web.setWebViewClient(new WebViewClient());
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
            web.loadUrl(START_URL);
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
