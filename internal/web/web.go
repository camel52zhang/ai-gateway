package web

import (
	"encoding/json"
	"strings"
)

// RenderLogin returns the standard login page HTML (vanilla JS, no external dependencies).
func RenderLogin() string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>AI Gateway Login</title>
  <style>
    * { margin:0; padding:0; box-sizing:border-box; }
    body { font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; background:#f5f5f5; display:flex; align-items:center; justify-content:center; min-height:100vh; }
    .card { background:#fff; padding:32px; border-radius:12px; box-shadow:0 4px 24px rgba(0,0,0,.08); width:min(92vw,400px); }
    h2 { font-size:22px; font-weight:700; text-align:center; margin-bottom:4px; }
    .sub { color:#888; font-size:14px; text-align:center; margin-bottom:24px; }
    label { display:block; font-size:13px; font-weight:500; margin-bottom:4px; color:#444; }
    input { width:100%; padding:10px 12px; border:1px solid #ddd; border-radius:8px; font-size:14px; outline:none; margin-bottom:16px; }
    input:focus { border-color:#000; box-shadow:0 0 0 2px rgba(0,0,0,.1); }
    button { width:100%; padding:10px; border:none; border-radius:8px; background:#000; color:#fff; font-size:14px; font-weight:500; cursor:pointer; }
    button:disabled { opacity:.5; cursor:not-allowed; }
    button:hover:not(:disabled) { background:#333; }
    .error { background:#fef2f2; color:#dc2626; padding:10px; border-radius:8px; font-size:13px; text-align:center; margin-bottom:16px; display:none; }
    .footer { text-align:center; margin-top:16px; font-size:12px; color:#aaa; }
    .spinner { display:none; }
  </style>
</head>
<body>
  <div class="card">
    <h2>登录到 AI 网关</h2>
    <p class="sub">请输入凭据以访问控制台</p>
    <div id="error" class="error"></div>
    <form id="login-form">
      <label for="username">用户名</label>
      <input id="username" type="text" required autocomplete="username">
      <label for="password">密码</label>
      <input id="password" type="password" required autocomplete="current-password">
      <button type="submit" id="submit-btn">登录</button>
    </form>
    <p class="footer">登录后可在「设置」页面修改密码</p>
  </div>
  <script>
    document.getElementById('login-form').addEventListener('submit', async function(e) {
      e.preventDefault();
      var btn = document.getElementById('submit-btn');
      var errDiv = document.getElementById('error');
      btn.disabled = true; btn.textContent = '登录中...';
      errDiv.style.display = 'none';
      try {
        var res = await fetch('/auth/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ username: document.getElementById('username').value, password: document.getElementById('password').value }),
        });
        if (res.ok) { window.location.href = '/'; return; }
        var d = await res.json();
        errDiv.textContent = d.error || '登录失败'; errDiv.style.display = 'block';
      } catch(e) { errDiv.textContent = '网络错误'; errDiv.style.display = 'block'; }
      finally { btn.disabled = false; btn.textContent = '登录'; }
    });
  </script>
</body>
</html>`
}

// RenderDashboard returns the full dashboard HTML with all tabs.
// providerDataJSON is a JSON object with "categories" and "providers" keys,
// injected into the page via {{PROVIDER_DATA}} placeholder replacement.
func RenderDashboard(providerDataJSON string) string {
	return renderDashboardTemplate(providerDataJSON)
}

// renderDashboardTemplate builds the full dashboard HTML, injecting providerDataJSON
// into the {{PROVIDER_DATA}} placeholder (without JSON escaping — providerDataJSON is
// already well-formed JSON from the caller).
func renderDashboardTemplate(providerDataJSON string) string {
	tpl := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>AI Gateway Dashboard</title>
  <script src="/static/tailwind.js"></script>
  <script src="/static/vue.global.js"></script>
  <link href="/static/all.min.css" rel="stylesheet">
  <style>
    :root { --radius:0.5rem; --primary:#000; --danger:#ef4444; --border:#e5e7eb; --success:#10b981; }
    body { font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif; background:#f9fafb; }
    .card { background:#fff; border-radius:var(--radius); border:1px solid var(--border); box-shadow:0 1px 2px rgba(0,0,0,.05); padding:1.5rem; margin-bottom:1.5rem; }
    .btn { padding:.5rem 1rem; border-radius:var(--radius); font-size:.875rem; font-weight:500; transition:all .2s; cursor:pointer; }
    .btn-primary { background:var(--primary); color:#fff; border:none; }
    .btn-primary:hover { background:#374151; }
    .btn-secondary { background:#fff; border:1px solid #d1d5db; color:#374151; }
    .btn-secondary:hover { background:#f3f4f6; }
    .btn-danger { color:#ef4444; background:transparent; border:none; cursor:pointer; }
    .btn-danger:hover { color:#dc2626; }
    .input { width:100%; border-radius:var(--radius); border:1px solid #d1d5db; padding:.5rem .75rem; font-size:.875rem; outline:none; transition:border-color .2s; }
    .input:focus { border-color:var(--primary); box-shadow:0 0 0 2px rgba(0,0,0,.1); }
    .nav-tab { padding:.5rem 1rem; font-size:.875rem; border-bottom:2px solid transparent; cursor:pointer; transition:all .2s; }
    .nav-tab.active { border-bottom-color:var(--primary); color:var(--primary); font-weight:600; }
    .stat-card { background:#f9fafb; border-radius:var(--radius); padding:1rem; text-align:center; }
    .stat-value { font-size:1.5rem; font-weight:700; }
    .stat-label { font-size:.75rem; color:#6b7280; margin-top:.25rem; }
    .health-dot { width:8px; height:8px; border-radius:50%; display:inline-block; margin-right:4px; }
    .health-healthy { background:var(--success); }
    .health-degraded { background:#f59e0b; }
    .health-circuit_open { background:var(--danger); }
    .toast { position:fixed; bottom:1.5rem; right:1.5rem; padding:.75rem 1.25rem; border-radius:var(--radius); color:#fff; font-size:.875rem; z-index:100; animation:slideUp .3s ease; box-shadow:0 4px 12px rgba(0,0,0,.15); }
    .toast-success { background:var(--success); }
    .toast-error { background:var(--danger); }
    .toast-info { background:#3b82f6; }
    @keyframes slideUp { from{transform:translateY(1rem);opacity:0} to{transform:translateY(0);opacity:1} }
    @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:.5} }
    .animate-pulse { animation:pulse 2s cubic-bezier(0.4,0,0.6,1) infinite; }
    .modal-overlay { position:fixed; inset:0; background:rgba(0,0,0,.5); display:flex; align-items:center; justify-content:center; z-index:50; }
    .modal { background:#fff; border-radius:var(--radius); padding:1.5rem; max-width:500px; width:90%; max-height:80vh; overflow-y:auto; }
  </style>
</head>
<body class="text-gray-800">
  <div id="app">
    <div v-if="loading" class="flex justify-center items-center min-h-screen">
      <i class="fas fa-circle-notch fa-spin text-3xl text-gray-400"></i>
    </div>

    <div v-else class="max-w-6xl mx-auto p-4 md:p-6">
      <!-- Header -->
      <header class="flex flex-col sm:flex-row justify-between items-start sm:items-center mb-6 gap-3">
        <div>
          <h1 class="text-2xl font-bold tracking-tight">AI 网关控制台</h1>
          <p class="text-gray-500 text-sm mt-1">统一管理 LLM 密钥、模型路由与监控</p>
        </div>
        <div class="flex gap-2">
          <button @click="regenerateKey" class="btn btn-secondary text-xs"><i class="fas fa-sync-alt mr-1"></i>重置密钥</button>
          <button @click="logout" class="btn btn-secondary text-xs"><i class="fas fa-sign-out-alt mr-1"></i>登出</button>
        </div>
      </header>

      <!-- Navigation Tabs -->
      <div class="flex border-b mb-6 overflow-x-auto">
        <div v-for="tab in tabs" :key="tab.id" @click="activeTab=tab.id"
          :class="['nav-tab whitespace-nowrap', activeTab===tab.id ? 'active' : 'text-gray-500 hover:text-gray-700']">
          <i :class="tab.icon" class="mr-1"></i>{{ tab.label }}
        </div>
      </div>

      <!-- Tab: Overview (概览) -->
      <div v-show="activeTab==='overview'">
        <div class="card relative overflow-hidden">
          <div class="absolute top-0 left-0 w-1 h-full bg-black"></div>
          <div class="flex justify-between items-start mb-4">
            <div>
              <h2 class="font-semibold text-lg">统一 API 密钥</h2>
              <p class="text-xs text-gray-500 mt-1">用作 OpenAI api_key，对发往本代理的请求进行身份验证</p>
            </div>
            <span class="bg-green-100 text-green-800 text-xs px-2 py-1 rounded-full font-medium">Active</span>
          </div>
          <div class="bg-gray-50 p-3 rounded-lg border border-gray-200 flex items-center justify-between mb-4">
            <code class="text-sm font-mono text-gray-700 break-all">{{ config.unifiedKey || '未生成' }}</code>
            <button @click="copyToClipboard(config.unifiedKey)" class="ml-2 text-gray-400 hover:text-black"><i class="far fa-copy"></i></button>
          </div>
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4 text-sm text-gray-600 bg-gray-50 p-4 rounded-lg">
            <div><span class="font-semibold inline-block w-20 text-xs">Base URL</span><code class="bg-white px-1 border rounded text-xs">{{ baseUrl }}/v1</code></div>
            <div><span class="font-semibold inline-block w-20 text-xs">对话</span><code class="bg-white px-1 border rounded text-xs">/v1/chat/completions</code></div>
          </div>
        </div>

        <div class="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
          <div class="stat-card"><div class="stat-value text-gray-900">{{ config.stats?.requests || 0 }}</div><div class="stat-label">总请求数</div></div>
          <div class="stat-card"><div class="stat-value text-purple-600">{{ formatNumber(config.stats?.tokens || 0) }}</div><div class="stat-label">总 Token</div></div>
          <div class="stat-card"><div class="stat-value text-blue-600">{{ formatNumber(config.stats?.promptTokens || 0) }}</div><div class="stat-label">Prompt Tokens</div></div>
          <div class="stat-card"><div class="stat-value text-green-600">{{ formatNumber(config.stats?.completionTokens || 0) }}</div><div class="stat-label">Completion Tokens</div></div>
        </div>

        <div class="card">
          <div class="flex justify-between items-center mb-4">
            <h3 class="font-semibold">系统健康摘要</h3>
            <span class="text-xs text-gray-400">最近趋势</span>
          </div>
          <div class="grid grid-cols-1 md:grid-cols-4 gap-3 mb-4">
            <div class="stat-card"><div class="stat-value" :class="healthSummaryStatusClass">{{ healthSummary.statusLabel }}</div><div class="stat-label">整体状态</div></div>
            <div class="stat-card"><div class="stat-value text-blue-600">{{ healthSummary.totalProviders }}</div><div class="stat-label">已配置 Provider</div></div>
            <div class="stat-card"><div class="stat-value text-red-600">{{ healthSummary.totalFailures }}</div><div class="stat-label">累计失败</div></div>
            <div class="stat-card"><div class="stat-value text-purple-600">{{ healthSummary.trend.requests.slice(-1)[0] || 0 }}</div><div class="stat-label">最新窗口请求</div></div>
          </div>
          <div class="flex flex-wrap gap-2 text-xs text-gray-500">
            <span class="bg-gray-100 px-2 py-1 rounded">健康: {{ healthSummary.healthyProviders }}</span>
            <span class="bg-gray-100 px-2 py-1 rounded">降级: {{ healthSummary.degradedProviders }}</span>
            <span class="bg-gray-100 px-2 py-1 rounded">熔断: {{ healthSummary.circuitOpenProviders }}</span>
          </div>
        </div>

        <!-- Provider 健康状态 -->
        <div class="card">
          <h3 class="font-semibold mb-4">Provider 健康状态</h3>
          <div class="space-y-2">
            <div v-for="p in config.providers" :key="p.type" class="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
              <div class="flex items-center gap-2">
                <span :class="['health-dot', 'health-' + (healthStatus(p.type))]"></span>
                <span class="font-medium capitalize text-sm">{{ p.type }}</span>
              </div>
              <div class="flex items-center gap-4 text-xs text-gray-500">
                <span v-if="latency(p.type) !== null"><i class="fas fa-clock mr-1"></i>{{ latency(p.type) }}ms</span>
                <span v-else class="text-gray-300">--</span>
                <span :class="{
                  'text-green-600': healthStatus(p.type) === 'healthy',
                  'text-yellow-600': healthStatus(p.type) === 'degraded',
                  'text-red-600': healthStatus(p.type) === 'circuit_open',
                }">{{ healthLabel(p.type) }}</span>
              </div>
            </div>
            <div v-if="!config.providers?.length" class="text-sm text-gray-400 p-4 text-center">暂无已配置 Provider</div>
          </div>
        </div>

        <!-- 失败统计 -->
        <div class="card">
          <div class="flex justify-between items-center mb-4">
            <h3 class="font-semibold">失败统计</h3>
            <span class="text-xs text-gray-400">按 Provider 汇总</span>
          </div>
          <div class="space-y-2">
            <div v-for="p in config.providers" :key="p.type + '-failure'" class="flex items-center justify-between p-2 bg-gray-50 rounded-lg">
              <div>
                <div class="font-medium capitalize text-sm">{{ p.type }}</div>
                <div class="text-xs text-gray-500">{{ failureSummary(p.type).total }} 次失败</div>
              </div>
              <div class="flex flex-wrap gap-2 text-xs">
                <span v-for="(value, key) in failureSummary(p.type).categories" :key="p.type + '-' + key" class="bg-white px-2 py-1 rounded border text-gray-600">
                  {{ failureCategoryLabel(key) }}: {{ value }}
                </span>
                <span v-if="!Object.keys(failureSummary(p.type).categories).length" class="text-gray-400">暂无失败</span>
              </div>
            </div>
            <div v-if="!config.providers?.length" class="text-sm text-gray-400 p-4 text-center">暂无已配置 Provider</div>
          </div>
        </div>
      </div>

      <!-- Tab: Provider (提供商) -->
      <div v-show="activeTab==='providers'">
        <div class="card">
          <h3 class="font-semibold mb-4">配置提供方</h3>
          <div class="mb-4">
            <input v-model="providerSearch" placeholder="搜索 Provider..." class="input mb-2">
            <div class="flex gap-2 flex-wrap">
              <button v-for="cat in categoryButtons" @click="selectedCategory=cat.key"
                :class="selectedCategory===cat.key ? 'bg-black text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200'"
                class="px-2 py-1 rounded text-xs transition">{{ cat.label }}</button>
            </div>
          </div>
          <div class="border rounded-lg divide-y max-h-64 overflow-y-auto mb-4">
            <div v-for="p in filteredProviders"
              @click="selectProvider(p)"
              :class="selectedProviderId===p.id ? 'bg-gray-50 border-l-4 border-l-black' : 'hover:bg-gray-50'"
              class="px-4 py-3 cursor-pointer transition flex items-center justify-between">
              <div>
                <div class="font-medium text-sm">{{ p.label }}</div>
                <div class="text-xs text-gray-400">{{ p.category }} · {{ p.adapter || 'openai' }}</div>
              </div>
              <span class="text-xs" :class="hasProviderKeyById(p.id) ? 'text-green-600' : 'text-gray-300'">
                <i :class="hasProviderKeyById(p.id) ? 'fas fa-key' : 'far fa-circle'"></i>
              </span>
            </div>
            <div v-if="!filteredProviders.length" class="px-4 py-6 text-center text-sm text-gray-400">未匹配到 Provider</div>
          </div>
          <div v-if="selectedProvider" class="p-4 bg-gray-50 rounded-lg space-y-3">
            <div class="flex justify-between items-start">
              <h4 class="font-medium">{{ selectedProvider.label }}</h4>
              <div class="flex gap-3 text-xs">
                <a v-if="selectedProvider.website" :href="selectedProvider.website" target="_blank" class="text-blue-600 hover:underline">官网</a>
                <a v-if="selectedProvider.docs" :href="selectedProvider.docs" target="_blank" class="text-blue-600 hover:underline">文档</a>
                <a v-if="selectedProvider.apiKeyUrl" :href="selectedProvider.apiKeyUrl" target="_blank" class="text-green-600 hover:underline">获取 Key</a>
              </div>
            </div>
            <div class="text-xs text-gray-500">BaseURL: <code class="bg-white px-1 border rounded">{{ selectedProvider.baseUrl || '—' }}</code></div>
            <div class="flex gap-2">
              <input v-model="newProvider.key" type="password" placeholder="粘贴 API Key" class="input flex-1">
              <button @click="addProvider" :disabled="savingKey" class="btn btn-primary whitespace-nowrap">
                <span v-if="!savingKey">{{ hasProviderKeyById(selectedProviderId) ? '更新密钥' : '添加密钥' }}</span>
                <span v-else><i class="fas fa-circle-notch fa-spin"></i></span>
              </button>
            </div>
          </div>
          <div v-if="config.providers?.length" class="mt-4 space-y-2">
            <div v-for="(item,index) in config.providers" class="flex items-center justify-between p-3 bg-gray-50 rounded-lg border border-gray-100">
              <div>
                <div class="font-medium text-sm capitalize">{{ item.type }}</div>
                <div class="text-xs text-gray-400">****{{ item.key ? item.key.slice(-4) : '----' }}</div>
              </div>
              <button @click="removeProvider(index)" class="btn-danger text-sm"><i class="fas fa-trash-alt"></i> 移除</button>
            </div>
          </div>

          <hr class="my-6 border-gray-200">
          <div class="flex justify-between items-center mb-4">
            <h3 class="font-semibold">自定义 Provider</h3>
            <button v-if="!showCustomForm" @click="openAddCustomForm" class="btn btn-secondary text-xs"><i class="fas fa-plus"></i> 添加</button>
            <button v-else @click="closeCustomForm" class="btn btn-secondary text-xs"><i class="fas fa-times"></i> {{ editingCustomId ? '取消编辑' : '取消' }}</button>
          </div>
          <form v-if="showCustomForm" @submit.prevent="addCustomProvider" class="p-4 bg-gray-50 rounded-lg space-y-3 mb-3">
            <div class="grid grid-cols-2 gap-3">
              <div><label class="block text-xs font-medium text-gray-600 mb-1">ID（唯一标识）*</label><input v-model="customForm.id" :disabled="editingCustomId" type="text" placeholder="如 my-provider" class="input" required></div>
              <div><label class="block text-xs font-medium text-gray-600 mb-1">显示名称 *</label><input v-model="customForm.label" type="text" placeholder="如 My Provider" class="input" required></div>
              <div class="col-span-2"><label class="block text-xs font-medium text-gray-600 mb-1">Base URL *</label><input v-model="customForm.baseUrl" type="text" placeholder="https://api.example.com/v1" class="input" required></div>
              <div><label class="block text-xs font-medium text-gray-600 mb-1">适配器</label>
                <select v-model="customForm.adapter" class="input"><option value="openai">OpenAI 兼容</option><option value="google">Google Gemini</option><option value="cohere">Cohere</option></select>
              </div>
              <div><label class="block text-xs font-medium text-gray-600 mb-1">优先级</label><input v-model.number="customForm.priority" type="number" min="1" max="100" class="input"></div>
              <div class="col-span-2"><label class="block text-xs font-medium text-gray-600 mb-1">模型列表（逗号分隔）</label><input v-model="customForm.modelsStr" type="text" placeholder="model-a, model-b" class="input"></div>
              <div class="col-span-2"><label class="block text-xs font-medium text-gray-600 mb-1">API Key（可选，用于拉取模型/代理请求）</label><input v-model="customForm.key" @input="customKeyChanged = true" type="password" :placeholder="editingCustomId ? '留空则保持现有密钥' : '粘贴 API Key'" class="input"></div>
            </div>
            <button type="submit" :disabled="customLoading" class="btn btn-primary text-sm w-full">
              <span v-if="!customLoading">{{ editingCustomId ? '保存修改' : '添加自定义 Provider' }}</span><span v-else><i class="fas fa-circle-notch fa-spin"></i> 提交中...</span>
            </button>
          </form>
          <div v-if="customProviders.length" class="space-y-2">
            <div v-for="cp in customProviders" class="flex items-center justify-between p-3 bg-yellow-50 rounded-lg border border-yellow-100">
              <div class="flex-1">
                <div class="font-medium text-sm">{{ cp.label }} <span class="text-xs text-yellow-600 bg-yellow-100 px-1 rounded">自定义</span></div>
                <div class="text-xs text-gray-400">{{ cp.id }} · {{ cp.baseUrl }}</div>
                <div v-if="cp.keyMask" class="text-xs text-green-600 mt-0.5"><i class="fas fa-key"></i> 已配置密钥 {{ cp.keyMask }}</div>
                <div class="mt-2 flex gap-2">
                  <input v-model="cp._key" type="password" placeholder="粘贴 API Key" class="input flex-1 text-xs py-1">
                  <button @click="updateCustomProviderKey(cp)" :disabled="cp._saving" class="btn btn-secondary text-xs whitespace-nowrap">
                    <span v-if="!cp._saving">更新密钥</span><span v-else><i class="fas fa-circle-notch fa-spin"></i></span>
                  </button>
                  <button @click="editCustomProvider(cp)" class="btn btn-secondary text-xs whitespace-nowrap"><i class="fas fa-edit"></i> 编辑</button>
                </div>
              </div>
              <button v-if="pendingDeleteId !== cp.id" @click="removeCustomProvider(cp.id)" class="btn-danger text-xs ml-3" title="删除"><i class="fas fa-trash-alt"></i></button>
              <span v-else class="flex items-center gap-1 ml-3">
                <button @click="confirmDelete" class="btn-danger text-xs">确认?</button>
                <button @click="cancelDelete" class="btn btn-secondary text-xs">取消</button>
              </span>
            </div>
          </div>
          <div v-else-if="!showCustomForm" class="text-xs text-gray-400 p-2">暂无自定义 Provider。点击"添加"来接入你自己的 API 服务。</div>
        </div>
      </div>

      <!-- Tab: Models (模型) -->
      <div v-show="activeTab==='models'">
        <div class="card">
          <div class="flex justify-between items-center mb-6">
            <h3 class="font-semibold">模型状态</h3>
            <div class="flex items-center gap-3">
              <label class="flex items-center gap-1.5 text-xs text-gray-500 cursor-pointer select-none" title="显示已隐藏（关闭启用）的模型，便于重新开启">
                <input type="checkbox" v-model="showHidden" class="w-3.5 h-3.5 accent-indigo-600">
                显示已隐藏模型
              </label>
              <button @click="refreshAllModels" class="btn btn-secondary text-xs"><i class="fas fa-sync-alt mr-1"></i>刷新全部</button>
            </div>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full text-sm text-left">
              <thead class="text-xs text-gray-500 uppercase bg-gray-50">
                <tr>
                  <th class="px-4 py-3">#</th>
                  <th class="px-4 py-3">模型</th>
                  <th class="px-4 py-3">提供商</th>
                  <th class="px-4 py-3">状态</th>
                  <th class="px-4 py-3">启用</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(model,idx) in visibleModels" :key="idx" class="border-b hover:bg-gray-50" :class="!model.enabled ? 'opacity-60' : ''">
                  <td class="px-4 py-3 text-gray-400">{{ idx + 1 }}</td>
                  <td class="px-4 py-3 font-medium">{{ model.name }}</td>
                  <td class="px-4 py-3 text-gray-500">{{ model.provider }}</td>
                  <td class="px-4 py-3">
                    <span v-if="model.enabled" class="bg-green-100 text-green-800 text-xs px-2 py-0.5 rounded-full">可用</span>
                    <span v-else class="bg-gray-100 text-gray-500 text-xs px-2 py-0.5 rounded-full">已隐藏</span>
                  </td>
                  <td class="px-4 py-3">
                    <button @click="toggleModel(model.type, model.name)" :title="model.enabled ? '点击隐藏' : '点击启用'" :class="model.enabled ? 'text-green-600' : 'text-gray-300'" class="text-xl leading-none">
                      <i :class="model.enabled ? 'fas fa-toggle-on' : 'fas fa-toggle-off'"></i>
                    </button>
                  </td>
                </tr>
                <tr v-if="!visibleModels.length">
                  <td colspan="5" class="px-4 py-8 text-center text-gray-400">添加 Provider 后，模型将显示在这里</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- Tab: Settings (设置) -->
      <div v-show="activeTab==='settings'">
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <div class="card">
            <h3 class="font-semibold mb-1">账号安全</h3>
            <p class="text-xs text-gray-500 mb-4">修改控制台登录密码</p>
            <div class="space-y-3 max-w-sm">
              <div><label class="block text-xs font-medium text-gray-700 mb-1">当前密码</label><input v-model="passwordForm.currentPassword" type="password" class="input" autocomplete="current-password"></div>
              <div><label class="block text-xs font-medium text-gray-700 mb-1">新密码（至少6位）</label><input v-model="passwordForm.newPassword" type="password" class="input" autocomplete="new-password"></div>
              <div><label class="block text-xs font-medium text-gray-700 mb-1">确认新密码</label><input v-model="passwordForm.confirmPassword" type="password" class="input" autocomplete="new-password"></div>
              <button @click="changePassword" :disabled="passwordLoading" class="btn btn-primary w-full">
                <span v-if="!passwordLoading">保存新密码</span><span v-else><i class="fas fa-circle-notch fa-spin mr-1"></i>保存中...</span>
              </button>
            </div>
          </div>
          <div class="card">
            <h3 class="font-semibold mb-4">会话信息</h3>
            <div class="space-y-3 text-sm">
              <div class="flex justify-between"><span class="text-gray-500">登录用户</span><span class="font-medium">admin</span></div>
              <div class="flex justify-between"><span class="text-gray-500">会话有效期</span><span class="font-medium">24 小时</span></div>
              <div class="flex justify-between"><span class="text-gray-500">密码存储</span><span class="font-medium">PBKDF2-SHA256 (100k 轮)</span></div>
              <div class="pt-3 border-t mt-3">
                <button @click="logout" class="btn btn-danger text-sm"><i class="fas fa-sign-out-alt mr-1"></i>退出登录</button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Tab: Logs (日志) -->
      <div v-show="activeTab==='logs'">
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <!-- Recent Requests -->
          <div class="card">
            <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
              <h3 class="font-semibold">最近请求</h3>
              <div class="flex flex-wrap gap-2">
                <select v-model="logRequestTimeFilter" class="text-xs border rounded px-2 py-1 bg-white">
                  <option value="all">全部时间</option><option value="1h">1小时</option><option value="6h">6小时</option><option value="24h">24小时</option><option value="7d">7天</option>
                </select>
                <select v-model="logRequestProviderFilter" class="text-xs border rounded px-2 py-1 bg-white">
                  <option value="">全部 Provider</option>
                  <option v-for="provider in logProviderOptions" :key="'req-'+provider" :value="provider">{{ provider }}</option>
                </select>
              </div>
            </div>
            <div class="space-y-2 max-h-64 overflow-y-auto">
              <div v-for="(log,idx) in filteredRequestLogs" :key="'req'+idx" class="flex flex-col gap-1 p-2 bg-gray-50 rounded text-xs">
                <div class="flex justify-between gap-2">
                  <div><span class="font-medium">{{ log.model }}</span><span class="text-gray-400 ml-2">{{ log.provider }}</span></div>
                  <div class="text-gray-500 text-right"><span>{{ log.tokens || 0 }} tokens</span><span class="ml-2">{{ log.latencyMs || 0 }}ms</span></div>
                </div>
                <div class="text-gray-400">{{ formatTime(log.timestamp) }} · requestId: {{ log.requestId || '—' }}</div>
              </div>
              <div v-if="!filteredRequestLogs.length" class="text-center text-gray-400 text-sm py-4">暂无匹配请求记录</div>
            </div>
          </div>

          <!-- Error Logs -->
          <div class="card">
            <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
              <h3 class="font-semibold">错误日志</h3>
              <div class="flex flex-wrap gap-2">
                <select v-model="logErrorTimeFilter" class="text-xs border rounded px-2 py-1 bg-white">
                  <option value="all">全部时间</option><option value="1h">1小时</option><option value="6h">6小时</option><option value="24h">24小时</option><option value="7d">7天</option>
                </select>
                <select v-model="logErrorProviderFilter" class="text-xs border rounded px-2 py-1 bg-white">
                  <option value="">全部 Provider</option>
                  <option v-for="provider in logProviderOptions" :key="'err-'+provider" :value="provider">{{ provider }}</option>
                </select>
                <select v-model="logErrorCategoryFilter" class="text-xs border rounded px-2 py-1 bg-white">
                  <option value="">全部分类</option>
                  <option value="timeout">超时</option><option value="rate_limit">限流</option><option value="upstream_error">上游错误</option>
                  <option value="client_error">客户端错误</option><option value="unknown">未知</option>
                </select>
                <select v-model="logErrorStatusFilter" class="text-xs border rounded px-2 py-1 bg-white">
                  <option value="all">全部状态码</option><option value="4xx">4xx</option><option value="5xx">5xx</option><option value="other">其他</option>
                </select>
              </div>
            </div>
            <div class="space-y-2 max-h-64 overflow-y-auto">
              <div v-for="(log,idx) in filteredErrorLogs" :key="'err'+idx"
                :class="log.status >= 500 ? 'bg-red-50 border-red-200' : 'bg-yellow-50 border-yellow-200'"
                class="flex flex-col gap-1 p-2 rounded text-xs border">
                <div class="flex justify-between gap-2">
                  <span class="font-medium">{{ log.provider }} / {{ log.model }}</span>
                  <span :class="log.status >= 500 ? 'text-red-600' : 'text-yellow-600'">HTTP {{ log.status || 'ERR' }}</span>
                </div>
                <div class="text-gray-500 break-words">{{ log.body || log.message || '' }}</div>
                <div class="flex flex-wrap gap-2 text-gray-400">
                  <span>{{ formatTime(log.timestamp) }}</span>
                  <span v-if="log.category" class="bg-white px-1 rounded">{{ errorCategoryLabel(log.category) }}</span>
                  <span v-if="log.requestId" class="bg-white px-1 rounded">requestId: {{ log.requestId }}</span>
                </div>
              </div>
              <div v-if="!filteredErrorLogs.length" class="text-center text-gray-400 text-sm py-4">
                <i class="fas fa-check-circle text-green-400 mr-1"></i>暂无匹配错误
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Tab: Test (测试 / Playground) -->
      <div v-show="activeTab==='test'">
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <div class="card space-y-4">
            <h3 class="font-semibold">模型测试</h3>
            <div>
              <label class="block text-xs font-medium text-gray-600 mb-1">选择模型</label>
              <select v-model="pgModel" class="input" @change="onPgModelChange">
                <option value="">-- 选择模型 --</option>
                <option value="auto">🚀 Auto（自动选择可用模型）</option>
                <optgroup v-for="ptype in configuredProviderTypes" :label="ptype" :key="ptype">
                  <option v-for="m in enabledModelsFor(ptype)" :value="m" :key="ptype+'-'+m">{{ m }}</option>
                </optgroup>
              </select>
            </div>
            <div><label class="block text-xs font-medium text-gray-600 mb-1">System Prompt</label><textarea v-model="pgSystem" rows="3" placeholder="你是一个有用的助手..." class="input resize-none"></textarea></div>
            <div><label class="block text-xs font-medium text-gray-600 mb-1">User Message</label><textarea v-model="pgUser" rows="5" placeholder="写一首关于编程的诗" class="input resize-none"></textarea></div>
            <div class="grid grid-cols-3 gap-3">
              <div><label class="block text-xs text-gray-500 mb-1">Temperature</label><input v-model.number="pgTemperature" type="range" min="0" max="2" step="0.1" class="w-full"><div class="text-xs text-gray-400 text-center">{{ pgTemperature }}</div></div>
              <div><label class="block text-xs text-gray-500 mb-1">Max Tokens</label><input v-model.number="pgMaxTokens" type="number" min="1" max="32000" class="input"></div>
              <div><label class="block text-xs text-gray-500 mb-1">Top P</label><input v-model.number="pgTopP" type="range" min="0" max="1" step="0.05" class="w-full"><div class="text-xs text-gray-400 text-center">{{ pgTopP }}</div></div>
            </div>
            <div class="flex gap-2">
              <button @click="sendTest" :disabled="pgLoading || !pgModel || !pgUser" class="btn btn-primary flex-1">
                <span v-if="!pgLoading"><i class="fas fa-paper-plane mr-1"></i>发送</span>
                <span v-else><i class="fas fa-circle-notch fa-spin mr-1"></i>请求中...</span>
              </button>
              <button @click="pgStreaming=!pgStreaming" :class="pgStreaming ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'" class="btn text-xs">
                <i :class="pgStreaming ? 'fas fa-toggle-on' : 'fas fa-toggle-off'"></i> Stream
              </button>
            </div>
          </div>
          <div class="card space-y-3">
            <h3 class="font-semibold">响应</h3>
            <div v-if="pgStreamingActive" class="flex items-center gap-2 text-xs text-green-600">
              <span class="flex w-2 h-2 bg-green-500 rounded-full animate-pulse"></span> 流式输出中...
            </div>
            <div v-if="pgReasoning" class="bg-amber-50 border border-amber-200 rounded-lg p-3 mb-2 text-xs text-amber-800 whitespace-pre-wrap font-mono"><div class="font-semibold mb-1 text-amber-600"><i class="fas fa-brain mr-1"></i>思考过程</div>{{ pgReasoning }}</div>
            <div class="bg-gray-50 border rounded-lg p-4 min-h-48 max-h-96 overflow-y-auto whitespace-pre-wrap font-mono text-sm leading-relaxed"
              v-text="pgOutput || '点击发送按钮来测试模型'"></div>
            <div v-if="pgStats" class="grid grid-cols-4 gap-2 text-xs">
              <div class="bg-gray-50 rounded p-2 text-center"><div class="font-bold text-purple-600">{{ pgStats.totalTokens || 0 }}</div><div class="text-gray-400">Tokens</div></div>
              <div class="bg-gray-50 rounded p-2 text-center"><div class="font-bold text-blue-600">{{ pgStats.promptTokens || 0 }}</div><div class="text-gray-400">Prompt</div></div>
              <div class="bg-gray-50 rounded p-2 text-center"><div class="font-bold text-green-600">{{ pgStats.completionTokens || 0 }}</div><div class="text-gray-400">Completion</div></div>
              <div class="bg-gray-50 rounded p-2 text-center"><div class="font-bold text-orange-600">{{ pgLatency }}ms</div><div class="text-gray-400">延迟</div></div>
            </div>
            <div v-if="pgError" class="p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
              <i class="fas fa-exclamation-triangle mr-1"></i>{{ pgError }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Toast -->
    <div v-if="toast.message" :class="['toast', 'toast-' + toast.type]">{{ toast.message }}</div>
  </div>

  <script>
    const { createApp } = Vue;
    const PM = {{PROVIDER_DATA}};

    createApp({
      data() {
        return {
          loading: true,
          baseUrl: window.location.origin,
          config: {
            unifiedKey: '',
            providers: [],
            stats: { requests: 0, tokens: 0, promptTokens: 0, completionTokens: 0 },
            requestLog: [],
            errorLog: [],
            providerLatency: {},
            providerHealth: {},
            failureMetrics: {},
            healthSummary: {
              status: 'unknown', statusLabel: '未知', totalProviders: 0,
              healthyProviders: 0, degradedProviders: 0, circuitOpenProviders: 0,
              totalFailures: 0, trend: { requests: [], failures: [] },
            },
          },
          newProvider: { type: '', key: '' },
          allProviders: PM.providers || [],
          providerCategories: PM.categories || {},
          providerSearch: '',
          selectedCategory: 'all',
          selectedProvider: null,
          selectedProviderId: '',
          providerModels: {},
          // modelEnabled: { "provider/model": bool } — user toggles; default on.
          modelEnabled: {},
          // When true, the Models table also shows hidden (disabled) models so
          // they can be toggled back on. Hidden models never enter the test
          // playground regardless of this flag.
          showHidden: false,
          activeTab: 'overview',
          tabs: [
            { id: 'overview',  label: '概览',   icon: 'fas fa-chart-bar' },
            { id: 'providers', label: '提供商',  icon: 'fas fa-plug' },
            { id: 'models',    label: '模型',   icon: 'fas fa-cubes' },
            { id: 'test',      label: '测试',   icon: 'fas fa-terminal' },
            { id: 'logs',      label: '日志',   icon: 'fas fa-list-alt' },
            { id: 'settings',  label: '设置',   icon: 'fas fa-cog' },
          ],
          toast: { message: '', type: 'success' },
          savingKey: false,
          // Password
          passwordForm: { currentPassword: '', newPassword: '', confirmPassword: '' },
          passwordLoading: false,
          // Custom provider
          showCustomForm: false,
          editingCustomId: '',
          customKeyChanged: false,
          customForm: { id: '', label: '', baseUrl: '', adapter: 'openai', priority: 50, modelsStr: '', key: '' },
          customLoading: false,
          customProviders: [],
          pendingDeleteId: '',
          // Test (playground)
          pgModel: '',
          pgSystem: '',
          pgUser: '',
          pgTemperature: 0.7,
          pgMaxTokens: 2048,
          pgTopP: 1,
          pgStreaming: true,
          pgStreamingActive: false,
          pgOutput: '',
          pgReasoning: '',
          pgStats: null,
          pgLatency: 0,
          pgError: '',
          pgLoading: false,
          pgAbortController: null,
          // Log filters
          logRequestTimeFilter: 'all',
          logRequestProviderFilter: '',
          logErrorTimeFilter: 'all',
          logErrorProviderFilter: '',
          logErrorCategoryFilter: '',
          logErrorStatusFilter: 'all',
        };
      },
      computed: {
        categoryButtons() {
          const cats = [{ key: 'all', label: '全部' }];
          for (const key of Object.keys(this.providerCategories || {})) {
            const first = this.providerCategories[key]?.[0];
            cats.push({ key, label: first?.category || key });
          }
          return cats;
        },
        filteredProviders() {
          let list = this.allProviders || [];
          if (this.selectedCategory !== 'all') list = list.filter(p => p.category === this.selectedCategory);
          if (this.providerSearch) {
            const s = this.providerSearch.toLowerCase();
            list = list.filter(p => (p.label||'').toLowerCase().includes(s) || (p.id||'').toLowerCase().includes(s));
          }
          return list;
        },
        activeModels() {
          const result = [];
          // Include custom providers too, so a freshly added custom provider's
          // pulled models show immediately without waiting for re-login.
          const addedTypes = new Set([
            ...(this.config.providers || []).map(p => p.type),
            ...(this.customProviders || []).map(cp => cp.id),
          ]);
          for (const type of addedTypes) {
            const def = this.allProviders.find(p => p.id === type);
            const label = def?.label || type;
            (this.providerModels[type] || []).forEach(m => {
              if (this.isModelEnabled(type, m)) result.push({ name: m, provider: label, type });
            });
          }
          return result;
        },
        // All fetched models regardless of enabled state, each tagged with its
        // current enabled flag. Used by the Models table when showHidden is on.
        allModels() {
          const result = [];
          const addedTypes = new Set([
            ...(this.config.providers || []).map(p => p.type),
            ...(this.customProviders || []).map(cp => cp.id),
          ]);
          for (const type of addedTypes) {
            const def = this.allProviders.find(p => p.id === type);
            const label = def?.label || type;
            (this.providerModels[type] || []).forEach(m => {
              result.push({ name: m, provider: label, type, enabled: this.isModelEnabled(type, m) });
            });
          }
          return result;
        },
        // Models table rows. Hidden models are omitted unless showHidden is on.
        visibleModels() {
          return this.showHidden ? this.allModels : this.allModels.filter(m => m.enabled);
        },
        configuredProviderTypes() {
          // Include custom providers too, so the Models module reflects a freshly
          // added custom provider without requiring a re-login (full config reload).
          return [...new Set([
            ...(this.config.providers || []).map(p => p.type),
            ...(this.customProviders || []).map(cp => cp.id),
          ])];
        },
        logProviderOptions() {
          const s = new Set([
            ...(this.config.providers || []).map(p => p.type),
            ...(this.config.requestLog || []).map(l => l.provider),
            ...(this.config.errorLog || []).map(l => l.provider),
          ].filter(Boolean));
          return [...s].sort();
        },
        filteredRequestLogs() {
          let logs = this.config.requestLog || [];
          if (this.logRequestProviderFilter) logs = logs.filter(log => (log.provider||'').toLowerCase() === this.logRequestProviderFilter.toLowerCase());
          if (this.logRequestTimeFilter !== 'all') logs = logs.filter(log => this.matchesTimeRange(log.timestamp, this.logRequestTimeFilter));
          return logs;
        },
        filteredErrorLogs() {
          let logs = this.config.errorLog || [];
          if (this.logErrorProviderFilter) logs = logs.filter(log => (log.provider||'').toLowerCase() === this.logErrorProviderFilter.toLowerCase());
          if (this.logErrorCategoryFilter) logs = logs.filter(log => (log.category||'unknown') === this.logErrorCategoryFilter);
          if (this.logErrorTimeFilter !== 'all') logs = logs.filter(log => this.matchesTimeRange(log.timestamp, this.logErrorTimeFilter));
          if (this.logErrorStatusFilter !== 'all') logs = logs.filter(log => this.matchesStatusFilter(log.status, this.logErrorStatusFilter));
          return logs;
        },
        healthSummary() {
          const s = this.config?.healthSummary || {};
          return {
            status: s.status || 'unknown', statusLabel: s.statusLabel || '未知',
            totalProviders: s.totalProviders || 0, healthyProviders: s.healthyProviders || 0,
            degradedProviders: s.degradedProviders || 0, circuitOpenProviders: s.circuitOpenProviders || 0,
            totalFailures: s.totalFailures || 0,
            trend: s.trend || { requests: [], failures: [] },
          };
        },
        healthSummaryStatusClass() {
          const m = { healthy:'text-green-600', degraded:'text-yellow-600', circuit_open:'text-red-600', unknown:'text-gray-500' };
          return m[this.healthSummary.status] || 'text-gray-500';
        },
      },
      async mounted() {
        try {
          await this.fetchConfig();
          await this.fetchCustomProviders();
        } catch(e) { console.error(e); }
        this.loading = false;
      },
      methods: {
        showToast(message, type = 'success') {
          this.toast = { message, type };
          setTimeout(() => { if (this.toast.message === message) this.toast.message = ''; }, 3000);
        },
        formatNumber(n) { return n >= 1e6 ? (n/1e6).toFixed(1)+'M' : n >= 1000 ? (n/1000).toFixed(1)+'K' : String(n); },
        formatTime(ts) { if (!ts) return ''; return new Date(ts).toLocaleTimeString('zh-CN', { hour:'2-digit', minute:'2-digit' }); },
        matchesTimeRange(ts, range) {
          if (!ts || range === 'all') return true;
          const cut = { '1h': Date.now()-3600000, '6h': Date.now()-21600000, '24h': Date.now()-86400000, '7d': Date.now()-604800000 }[range];
          return new Date(ts).getTime() >= cut;
        },
        matchesStatusFilter(status, filter) {
          const code = Number(status);
          if (filter === '4xx') return Number.isFinite(code) && code >= 400 && code < 500;
          if (filter === '5xx') return Number.isFinite(code) && code >= 500;
          if (filter === 'other') return !Number.isFinite(code) || code < 400 || code >= 600;
          return true;
        },
        healthStatus(type) { return (this.config.providerHealth||{})[type] || 'healthy'; },
        healthLabel(type) {
          const m = { healthy:'正常', degraded:'降级', circuit_open:'熔断' };
          return m[this.healthStatus(type)] || '未知';
        },
        errorCategoryLabel(key) {
          const m = { timeout:'超时', rate_limit:'限流', upstream_error:'上游错误', client_error:'客户端错误', unknown:'未知' };
          return m[key] || key;
        },
        failureCategoryLabel(key) {
          const m = { timeout:'超时', rate_limit:'限流', upstream_error:'上游错误', client_error:'客户端错误', unknown:'未知' };
          return m[key] || key;
        },
        latency(type) { const v = (this.config.providerLatency||{})[type]; return v !== undefined && v !== null ? v : null; },
        failureSummary(type) {
          const e = (this.config.failureMetrics || {})[type] || {};
          return { total: e.total || 0, categories: e.categories || {} };
        },
        hasProviderKeyById(id) {
          return (this.config.providers || []).some(x => x.type === id);
        },
        copyToClipboard(text) {
          if (!text) return;
          navigator.clipboard.writeText(text).then(() => this.showToast('已复制到剪贴板')).catch(() => this.showToast('复制失败', 'error'));
        },
        isModelEnabled(type, model) {
          // Default on: absent key or explicit true means enabled.
          const v = (this.config.modelEnabled || {})[type + '/' + model];
          return v === undefined ? true : v;
        },
        enabledModelsFor(type) {
          return (this.providerModels[type] || []).filter(m => this.isModelEnabled(type, m));
        },
        async toggleModel(type, model) {
          if (!this.config.modelEnabled) this.config.modelEnabled = {};
          const key = type + '/' + model;
          this.config.modelEnabled[key] = !this.isModelEnabled(type, model);
          try {
            await this.saveConfig();
            const st = this.config.modelEnabled[key] ? '已启用' : '已隐藏';
            this.showToast(model + ' ' + st);
          } catch(e) { this.showToast('保存失败', 'error'); }
        },
        // Config
        async fetchConfig() {
          try {
            const res = await fetch('/api/config');
            if (res.ok) {
              const data = await res.json();
              this.config = { ...this.config, ...data };
              this.providerModels = data.models || {};
              for (const p of (this.config.providers || [])) await this.fetchModels(p.type, false);
            }
            await this.fetchStats();
          } catch(e) { console.error('Fetch config error:', e); }
        },
        async fetchStats() {
          try {
            const res = await fetch('/api/stats');
            if (res.ok) {
              const data = await res.json();
              this.config = {
                ...this.config,
                failureMetrics: data.failureMetrics || this.config.failureMetrics || {},
                providerLatency: data.providerLatency || this.config.providerLatency || {},
                providerHealth: data.providerHealth || this.config.providerHealth || {},
                stats: data.stats || this.config.stats || {},
                requestLog: data.requestLog || this.config.requestLog || [],
                errorLog: data.errorLog || this.config.errorLog || [],
                healthSummary: data.healthSummary || this.config.healthSummary || {},
              };
            }
          } catch(e) { console.error('Fetch stats error:', e); }
        },
        async saveConfig() {
          try {
            const res = await fetch('/api/config', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(this.config) });
            if (res.ok) return await res.json();
          } catch(e) { console.error('Save error:', e); }
          return null;
        },
        // Key
        async regenerateKey() {
          try {
            const res = await fetch('/api/key/regenerate', { method:'POST' });
            const data = await res.json();
            if (data.success) { this.config.unifiedKey = data.unifiedKey; this.showToast('密钥已重置！'); }
          } catch { this.showToast('重置失败', 'error'); }
        },
        // Provider
        selectProvider(p) {
          this.selectedProvider = p;
          this.selectedProviderId = p.id;
          this.newProvider.type = p.id;
        },
        async addProvider() {
          if (!this.newProvider.key) { this.showToast('请输入 API Key', 'error'); return; }
          if (!this.config.providers) this.config.providers = [];
          const ptype = this.newProvider.type;
          const pkey = this.newProvider.key;
          const idx = this.config.providers.findIndex(x => x.type === ptype);
          if (idx >= 0) this.config.providers[idx].key = pkey;
          else this.config.providers.push({ type: ptype, key: pkey });
          this.newProvider.key = '';
          this.selectedProvider = null;
          this.selectedProviderId = '';
          this.savingKey = true;
          try {
            await this.saveConfig();
            await this.fetchModels(ptype);
            this.showToast('Provider 已保存');
          } catch(e) { this.showToast('保存失败', 'error'); }
          finally { this.savingKey = false; }
        },
        removeProvider(index) {
          this.config.providers.splice(index, 1);
          this.saveConfig();
          this.showToast('Provider 已移除');
        },
        // Models
        async fetchModels(type, save = false) {
          try {
            const res = await fetch('/api/models?type=' + encodeURIComponent(type));
            const data = await res.json().catch(() => ({}));
            if (res.ok) {
              this.providerModels[type] = data.models || [];
              if (data.error) this.showToast(type + ': ' + data.error, 'error');
              if (save) this.saveConfig();
            } else if (data.error) {
              this.showToast(type + ': ' + data.error, 'error');
            }
          } catch(e) { console.error('Fetch models error:', e); }
        },
        async refreshAllModels() {
          const types = [
            ...(this.config.providers || []).map(p => p.type),
            ...(this.customProviders || []).map(cp => cp.id),
          ];
          for (const t of types) await this.fetchModels(t, false);
          this.showToast('模型列表已刷新');
        },
        // Custom providers
        async fetchCustomProviders() {
          try {
            const res = await fetch('/api/providers/custom');
            if (res.ok) {
              const data = await res.json();
              this.customProviders = data.custom || [];
            }
          } catch(e) { console.error('Fetch custom providers error:', e); }
        },
        openAddCustomForm() {
          // Always enter a clean ADD state: clear any lingering edit state so the
          // ID field is editable. Setting editingCustomId to null (not '') keeps the
          // ID input enabled, because :disabled="editingCustomId" treats '' as a
          // present (truthy) value in Vue 3 but null as absent (falsy).
          this.editingCustomId = null;
          this.customKeyChanged = false;
          this.customForm = { id:'', label:'', baseUrl:'', adapter:'openai', priority:50, modelsStr:'', key:'' };
          this.showCustomForm = true;
        },
        closeCustomForm() {
          this.showCustomForm = false;
          this.editingCustomId = null;
          this.customKeyChanged = false;
          this.customForm = { id:'', label:'', baseUrl:'', adapter:'openai', priority:50, modelsStr:'', key:'' };
        },
        editCustomProvider(cp) {
          this.editingCustomId = cp.id;
          this.customKeyChanged = false;
          this.customForm = {
            id: cp.id, label: cp.label, baseUrl: cp.baseUrl,
            adapter: cp.adapter || 'openai', priority: cp.priority || 50,
            modelsStr: (cp.models || []).join(', '), key: ''
          };
          this.showCustomForm = true;
        },
        async addCustomProvider() {
          if (!this.customForm.id || !this.customForm.label || !this.customForm.baseUrl) {
            this.showToast('请填写 ID、名称和 Base URL', 'error'); return;
          }
          this.customLoading = true;
          const isEdit = !!this.editingCustomId;
          try {
            // In edit mode, the key is server-side only. Only send it when the
            // user actually typed a new value; otherwise omit so the existing
            // key is preserved (the backend clears the key on an empty string).
            const body = {
              id: this.customForm.id, label: this.customForm.label, baseUrl: this.customForm.baseUrl,
              adapter: this.customForm.adapter, priority: this.customForm.priority,
              models: this.customForm.modelsStr ? this.customForm.modelsStr.split(',').map(s => s.trim()).filter(Boolean) : [],
            };
            if (!isEdit || this.customKeyChanged) {
              body.key = this.customForm.key || '';
            }
            const res = await fetch('/api/providers/custom', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body) });
            if (res.ok) {
              this.showToast(isEdit ? '自定义 Provider 已更新' : '自定义 Provider 添加成功');
              this.showCustomForm = false;
              this.editingCustomId = '';
              this.customKeyChanged = false;
              const newId = this.customForm.id;
              this.customForm = { id:'', label:'', baseUrl:'', adapter:'openai', priority:50, modelsStr:'', key:'' };
              await this.fetchCustomProviders();
              if (newId) await this.fetchModels(newId, false);
            } else {
              const d = await res.json();
              this.showToast(d.error || (isEdit ? '更新失败' : '添加失败'), 'error');
            }
          } catch { this.showToast('网络错误', 'error'); }
          finally { this.customLoading = false; }
        },
        async updateCustomProviderKey(cp) {
          // Send only the id + key; the backend merges into the existing custom
          // provider and mirrors the key into config.Providers for routing/models.
          if (cp._key === undefined || cp._key === null) cp._key = '';
          cp._saving = true;
          try {
            const res = await fetch('/api/providers/custom', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({ id: cp.id, key: cp._key }) });
            if (res.ok) {
              this.showToast(cp._key ? '密钥已更新' : '密钥已清除');
              cp._key = '';
              await this.fetchModels(cp.id, false);
            } else {
              const d = await res.json();
              this.showToast(d.error || '更新失败', 'error');
            }
          } catch { this.showToast('网络错误', 'error'); }
          finally { cp._saving = false; }
        },
        removeCustomProvider(id) {
          // Open an in-app confirmation (native confirm() is suppressed inside some webview previews).
          this.pendingDeleteId = id;
        },
        cancelDelete() {
          this.pendingDeleteId = '';
        },
        async confirmDelete() {
          const id = this.pendingDeleteId;
          this.pendingDeleteId = '';
          if (!id) return;
          try {
            const res = await fetch('/api/providers/custom?id=' + encodeURIComponent(id), { method:'DELETE' });
            if (res.ok) { this.showToast('已删除'); await this.fetchCustomProviders(); }
            else { const d = await res.json(); this.showToast(d.error || '删除失败', 'error'); }
          } catch { this.showToast('网络错误', 'error'); }
        },
        // Test (Playground)
        async sendTest() {
          if (!this.pgModel || !this.pgUser) { this.showToast('请选择模型并输入消息', 'error'); return; }
          this.pgLoading = true;
          this.pgOutput = '';
          this.pgReasoning = '';
          this.pgStats = null;
          this.pgLatency = 0;
          this.pgError = '';
          this.pgStreamingActive = this.pgStreaming;
          const startTime = Date.now();
          try {
            const messages = [];
            if (this.pgSystem) messages.push({ role:'system', content:this.pgSystem });
            messages.push({ role:'user', content:this.pgUser });
            const body = {
              model: this.pgModel, messages,
              temperature: this.pgTemperature, max_tokens: this.pgMaxTokens, top_p: this.pgTopP,
              stream: this.pgStreaming,
            };
            if (this.pgStreaming) {
              this.pgAbortController = new AbortController();
              const res = await fetch('/v1/chat/completions', {
                method:'POST', headers:{'Content-Type':'application/json', 'Authorization':'Bearer '+(this.config.unifiedKey||'')},
                body:JSON.stringify(body), signal:this.pgAbortController.signal,
              });
              if (!res.ok) { const txt = await res.text(); throw new Error(res.status+': '+(txt||'请求失败').substring(0,500)); }
              const reader = res.body.getReader();
              const decoder = new TextDecoder();
              let buf = '', usage = null, finished = false;
              // applyChunk parses one SSE/JSON line and folds its content into
              // the response. Tolerant of:
              //   - "data: {json}" AND "data:{json}" (some providers omit the space)
              //   - raw JSON lines / Ollama NDJSON (no "data:" prefix)
              //   - OpenAI streaming deltas OR a single non-streaming completion
              //     object (a provider that ignores stream:true still renders).
              const applyChunk = (raw) => {
                let payload = raw;
                if (/^data:/i.test(payload)) payload = payload.slice(5).replace(/^\s/, '');
                else if (!/^[\[{]/.test(payload)) return; // comment / other SSE field
                if (payload === '[DONE]') { finished = true; return; }
                try {
                  const chunk = JSON.parse(payload);
                  const delta = chunk.choices?.[0]?.delta;
                  if (delta) {
                    if (delta.reasoning_content) this.pgReasoning += delta.reasoning_content;
                    if (delta.content) this.pgOutput += delta.content;
                  }
                  const msg = chunk.choices?.[0]?.message;
                  if (msg) {
                    if (msg.reasoning_content) this.pgReasoning += msg.reasoning_content;
                    if (msg.content) this.pgOutput += msg.content;
                  }
                  if (chunk.response) this.pgOutput += chunk.response; // Ollama NDJSON
                  if (chunk.usage) usage = chunk.usage;
                } catch {}
              };
              while (true) {
                const {done, value} = await reader.read();
                if (done) break;
                buf += decoder.decode(value, {stream:true});
                const lines = buf.split('\n');
                buf = lines.pop() || '';
                for (const line of lines) {
                  const t = line.trim();
                  if (t) applyChunk(t);
                  if (finished) break;
                }
                if (finished) break;
              }
              // Flush any trailing bytes not terminated by a newline.
              if (buf.trim()) applyChunk(buf.trim());
              this.pgStreamingActive = false;
              this.pgLatency = Date.now() - startTime;
              if (usage) this.pgStats = { promptTokens:usage.prompt_tokens||0, completionTokens:usage.completion_tokens||0, totalTokens:usage.total_tokens||0 };
            } else {
              const res = await fetch('/v1/chat/completions', {
                method:'POST', headers:{'Content-Type':'application/json', 'Authorization':'Bearer '+(this.config.unifiedKey||'')},
                body:JSON.stringify(body),
              });
              if (!res.ok) { const txt = await res.text(); throw new Error(res.status+': '+(txt||'请求失败').substring(0,500)); }
              const data = await res.json();
              const msg = data.choices?.[0]?.message || {};
              this.pgOutput = msg.content || msg.reasoning_content || '(无内容)';
              if (msg.reasoning_content) this.pgReasoning = msg.reasoning_content;
              this.pgLatency = Date.now() - startTime;
              const u = data.usage;
              if (u) this.pgStats = { promptTokens:u.prompt_tokens||0, completionTokens:u.completion_tokens||0, totalTokens:u.total_tokens||0 };
            }
            this.pgAbortController = null;
          } catch(e) {
            if (e.name === 'AbortError') { this.pgError = '请求已取消'; this.pgStreamingActive = false; }
            else this.pgError = e.message;
            this.pgLatency = Date.now() - startTime;
          } finally { this.pgLoading = false; }
        },
        cancelTest() {
          if (this.pgAbortController) { this.pgAbortController.abort(); this.pgAbortController = null; }
        },
        onPgModelChange() {
          this.pgOutput = ''; this.pgReasoning = ''; this.pgStats = null; this.pgError = ''; this.pgLatency = 0;
        },
        // Password
        async changePassword() {
          const { currentPassword, newPassword, confirmPassword } = this.passwordForm;
          if (!currentPassword) { this.showToast('请输入当前密码', 'error'); return; }
          if (!newPassword || newPassword.length < 6) { this.showToast('新密码至少需要6位字符', 'error'); return; }
          if (newPassword !== confirmPassword) { this.showToast('两次输入的新密码不一致', 'error'); return; }
          this.passwordLoading = true;
          try {
            const res = await fetch('/auth/reset-password', {
              method:'POST', headers:{'Content-Type':'application/json'},
              body:JSON.stringify({ username:'admin', currentPassword, newPassword }),
            });
            if (res.ok) {
              this.showToast('密码修改成功，请重新登录', 'success');
              this.passwordForm = { currentPassword:'', newPassword:'', confirmPassword:'' };
              setTimeout(() => { window.location.href = '/login'; }, 1500);
            } else { const d = await res.json(); this.showToast(d.error || '修改失败', 'error'); }
          } catch { this.showToast('网络错误', 'error'); }
          finally { this.passwordLoading = false; }
        },
        async logout() {
          await fetch('/auth/logout', { method:'POST' });
          window.location.href = '/login';
        },
      },
    }).mount('#app');
  </script>
</body>
</html>`

	return strings.Replace(tpl, "{{PROVIDER_DATA}}", providerDataJSON, 1)
}

// escapeJSON returns a JSON-safe string suitable for embedding in HTML/JS contexts.
func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)[1 : len(b)-1]
}
