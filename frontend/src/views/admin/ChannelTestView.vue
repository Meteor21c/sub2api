<template>
  <AppLayout>
    <div class="space-y-4">
      <header class="relative overflow-hidden rounded-2xl bg-gradient-to-br from-slate-950 via-slate-900 to-indigo-950 px-5 py-3 text-white shadow-sm sm:px-6">
        <div class="relative z-10 flex flex-wrap items-start justify-between gap-3">
          <div class="max-w-3xl">
            <div class="mb-1 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.2em] text-indigo-200">
              <Icon name="beaker" size="sm" />
              {{ t('admin.channelTest.eyebrow') }}
            </div>
            <h1 class="text-xl font-semibold tracking-tight sm:text-2xl">{{ t('admin.channelTest.title') }}</h1>
            <p class="mt-1 max-w-2xl text-xs leading-5 text-slate-300 sm:text-sm">
              {{ t('admin.channelTest.description') }}
            </p>
          </div>
          <button
            type="button"
            data-testid="availability-refresh"
            class="inline-flex items-center gap-2 rounded-xl border border-white/15 bg-white/10 px-3 py-2 text-sm font-medium text-white transition hover:bg-white/15 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="loadingGroups || loadingCatalog || running || sendPending"
            @click="refreshAll"
          >
            <Icon name="refresh" size="sm" :class="loadingGroups || loadingCatalog ? 'animate-spin' : ''" />
            {{ t('admin.channelTest.refresh') }}
          </button>
        </div>
        <div class="pointer-events-none absolute -right-12 -top-16 h-48 w-48 rounded-full bg-indigo-500/20 blur-3xl" aria-hidden="true"></div>
        <div class="pointer-events-none absolute bottom-[-5rem] left-1/3 h-40 w-64 rounded-full bg-cyan-400/10 blur-3xl" aria-hidden="true"></div>
      </header>

      <section class="card grid gap-3 p-3 lg:grid-cols-[minmax(10rem,0.7fr)_minmax(17rem,1.1fr)_minmax(13rem,1fr)] lg:items-end" data-testid="availability-group-panel">
        <div class="flex flex-wrap items-start justify-between gap-2 lg:block">
          <div>
            <div class="flex items-center gap-2">
              <span class="flex h-8 w-8 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600 dark:bg-indigo-900/30 dark:text-indigo-300">
                <Icon name="server" size="sm" />
              </span>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.channelTest.groupTitle') }}</h2>
            </div>
            <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.groupHint') }}</p>
          </div>
          <div v-if="selectedGroup" class="flex flex-wrap items-center gap-2 text-xs lg:mt-2">
            <span class="badge badge-gray">{{ selectedGroup.platform }}</span>
            <span :class="selectedGroup.status === 'active' ? 'badge badge-success' : 'badge badge-danger'">
              {{ selectedGroup.status }}
            </span>
          </div>
        </div>

        <div class="contents">
          <label class="block">
            <span class="input-label">{{ t('admin.channelTest.activeGroup') }}</span>
            <select
              v-model.number="selectedGroupId"
              data-testid="availability-group"
              class="input w-full"
              :disabled="loadingGroups || loadingCatalog || running || sendPending"
            >
              <option :value="null">{{ t('admin.channelTest.groupPlaceholder') }}</option>
              <option v-for="group in groups" :key="group.id" :value="group.id">
                {{ group.name }} · {{ group.platform }}
              </option>
            </select>
          </label>
          <div class="rounded-xl border border-gray-200 bg-gray-50/80 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/40">
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
              <span>{{ t('admin.channelTest.scheduler') }}</span>
              <span v-if="loadingCatalog" class="text-indigo-600 dark:text-indigo-300">{{ t('admin.channelTest.loading') }}</span>
              <span v-else-if="catalog" class="text-gray-700 dark:text-gray-200">{{ t('admin.channelTest.serverRank') }}</span>
            </div>
            <p class="mt-1 text-sm leading-5 text-gray-600 dark:text-gray-300">
              {{ catalog?.scheduling_note || t('admin.channelTest.schedulerDescription') }}
            </p>
          </div>
        </div>

        <div v-if="pageError" class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200 lg:col-span-3" role="alert">
          {{ pageError }}
        </div>
      </section>

      <div v-if="selectedGroupId == null" class="card flex min-h-56 items-center justify-center border-dashed p-8 text-center">
        <div>
          <span class="mx-auto flex h-11 w-11 items-center justify-center rounded-2xl bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-gray-400">
            <Icon name="server" size="md" />
          </span>
          <p class="mt-3 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.chooseGroup') }}</p>
        </div>
      </div>

      <template v-else>
        <div class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2 lg:items-stretch">
            <section class="card flex min-h-0 flex-col overflow-hidden lg:h-[calc(100vh-23rem)] lg:min-h-[28rem] lg:max-h-[40rem]" data-testid="availability-accounts-panel">
              <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
                <div class="flex items-start justify-between gap-3">
                  <div>
                    <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.channelTest.accountsTitle') }}</h2>
                    <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.accountsHint') }}</p>
                  </div>
                  <span class="badge badge-gray whitespace-nowrap">{{ t('admin.channelTest.accountsCount', { count: accounts.length }) }}</span>
                </div>
                <label class="relative mt-3 block">
                  <span class="sr-only">{{ t('admin.channelTest.searchAccounts') }}</span>
                  <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                  <input
                    v-model="accountSearch"
                    data-testid="availability-account-search"
                    type="search"
                    class="input w-full pl-9"
                    :placeholder="t('admin.channelTest.searchAccounts')"
                  />
                </label>
              </div>

              <div class="availability-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto p-2.5">
                <button
                  type="button"
                  data-testid="availability-account-auto"
                  class="w-full rounded-2xl border p-3 text-left transition"
                  :class="selectedAccountId == null
                    ? 'border-indigo-400 bg-indigo-50/80 ring-2 ring-indigo-100 dark:border-indigo-500 dark:bg-indigo-900/20 dark:ring-indigo-900/40'
                    : 'border-gray-200 bg-white hover:border-indigo-300 hover:bg-indigo-50/40 dark:border-dark-700 dark:bg-dark-900 dark:hover:border-indigo-700 dark:hover:bg-indigo-900/10'"
                  :disabled="running || sendPending"
                  :aria-pressed="selectedAccountId == null"
                  @click="selectAutomaticAccount"
                >
                  <div class="flex items-start gap-3">
                    <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300">
                      <Icon name="arrowsUpDown" size="sm" />
                    </span>
                    <span class="min-w-0">
                      <span class="flex flex-wrap items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                        {{ t('admin.channelTest.autoRoute') }}
                        <span class="badge badge-gray">{{ t('admin.channelTest.serverRank') }}</span>
                      </span>
                      <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.autoRouteHint') }}</span>
                    </span>
                  </div>
                </button>

                <div v-if="loadingCatalog && !accounts.length" class="space-y-2 p-1" aria-live="polite">
                  <div v-for="skeleton in 3" :key="skeleton" class="h-32 animate-pulse rounded-2xl bg-gray-100 dark:bg-dark-800"></div>
                </div>
                <div v-else-if="!accounts.length" class="rounded-2xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  {{ t('admin.channelTest.noAccounts') }}
                </div>
                <div v-else-if="!filteredAccounts.length" class="rounded-2xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  {{ t('admin.channelTest.noMatchingAccounts') }}
                </div>

                <article
                  v-for="account in filteredAccounts"
                  :key="account.id"
                  class="rounded-xl border p-2.5 transition"
                  :class="selectedAccountId === account.id
                    ? 'border-indigo-400 bg-indigo-50/70 ring-2 ring-indigo-100 dark:border-indigo-500 dark:bg-indigo-900/20 dark:ring-indigo-900/40'
                    : 'border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900'"
                >
                  <button
                    type="button"
                    class="w-full text-left"
                    :data-testid="`availability-account-${account.id}`"
                    :disabled="running || sendPending"
                    :aria-pressed="selectedAccountId === account.id"
                    @click="selectAccount(account)"
                  >
                    <span class="flex items-start gap-2.5">
                      <span class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-[11px] font-bold tabular-nums text-gray-600 dark:bg-dark-800 dark:text-gray-300" :title="t('admin.channelTest.rank')">
                        #{{ account.rank }}
                      </span>
                      <span class="min-w-0 flex-1">
                        <span class="flex items-center gap-1.5 text-sm font-semibold text-gray-900 dark:text-white">
                          <span class="truncate" :title="account.name">{{ account.name }}</span>
                          <span class="shrink-0" :class="account.eligible ? 'badge badge-success' : 'badge badge-danger'">
                            {{ account.eligible ? t('admin.channelTest.eligible') : t('admin.channelTest.unavailable') }}
                          </span>
                        </span>
                        <span class="mt-0.5 flex flex-wrap items-center gap-x-1.5 text-[11px] text-gray-500 dark:text-gray-400">
                          <span>{{ account.platform }} · {{ account.status }}</span>
                          <span class="rounded bg-gray-100 px-1.5 py-0.5 tabular-nums dark:bg-dark-800">
                            {{ t('admin.channelTest.groupPriority') }} {{ account.group_priority }}
                          </span>
                        </span>
                      </span>
                    </span>
                  </button>

                  <div class="mt-2 grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-end gap-2" @click.stop>
                    <label class="block">
                      <span class="mb-0.5 block text-[10px] font-medium text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.priority') }}</span>
                      <input
                        :value="accountFieldValue(account, 'priority')"
                        :data-testid="`availability-account-${account.id}-priority`"
                        type="number"
                        min="0"
                        step="1"
                        class="input w-full px-2 py-1 text-xs"
                        :disabled="isAccountFieldSaving(account.id, 'priority') || running || sendPending"
                        @input="updateAccountDraft(account, 'priority', $event)"
                        @blur="commitAccountField(account, 'priority')"
                        @keydown.enter.prevent="commitAccountField(account, 'priority')"
                        @keydown.esc="cancelAccountField(account, 'priority')"
                      />
                    </label>
                    <label class="block">
                      <span class="mb-0.5 block text-[10px] font-medium text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.loadFactor') }}</span>
                      <input
                        :value="accountFieldValue(account, 'load_factor')"
                        :data-testid="`availability-account-${account.id}-load-factor`"
                        type="number"
                        min="0"
                        max="10000"
                        step="1"
                        class="input w-full px-2 py-1 text-xs"
                        :placeholder="account.load_factor == null ? '—' : undefined"
                        :disabled="isAccountFieldSaving(account.id, 'load_factor') || running || sendPending"
                        @input="updateAccountDraft(account, 'load_factor', $event)"
                        @blur="commitAccountField(account, 'load_factor')"
                        @keydown.enter.prevent="commitAccountField(account, 'load_factor')"
                        @keydown.esc="cancelAccountField(account, 'load_factor')"
                      />
                    </label>
                    <div class="pb-1 text-right text-[10px] text-gray-500 dark:text-gray-400">
                      <span class="block whitespace-nowrap">{{ t('admin.channelTest.concurrency') }}</span>
                      <strong class="text-xs tabular-nums text-gray-800 dark:text-gray-100">{{ formatConcurrency(account) }}</strong>
                    </div>
                  </div>
                  <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[10px]" aria-live="polite">
                    <span v-if="accountFieldState(account.id, 'priority') === 'saving' || accountFieldState(account.id, 'load_factor') === 'saving'" class="text-indigo-600 dark:text-indigo-300">
                      {{ t('admin.channelTest.saving') }}
                    </span>
                    <span v-else-if="accountFieldState(account.id, 'priority') === 'saved' || accountFieldState(account.id, 'load_factor') === 'saved'" class="text-emerald-600 dark:text-emerald-300">
                      <Icon name="checkCircle" size="xs" class="mr-1 inline-block align-[-2px]" />{{ t('admin.channelTest.saved') }}
                    </span>
                    <span v-if="accountFieldError(account.id, 'priority') || accountFieldError(account.id, 'load_factor')" class="text-red-600 dark:text-red-300">
                      {{ accountFieldError(account.id, 'priority') || accountFieldError(account.id, 'load_factor') }}
                    </span>
                  </div>
                  <p v-if="!account.eligible && account.reason" class="mt-2 text-xs leading-5 text-red-600 dark:text-red-300">
                    {{ account.reason }}
                  </p>
                </article>
              </div>

              <div class="border-t border-amber-200 bg-amber-50 px-3 py-2 text-[11px] leading-4 text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/10 dark:text-amber-200">
                <div class="flex items-start gap-2">
                  <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
                  <span><strong>{{ t('admin.channelTest.impactWarning') }}</strong> {{ t('admin.channelTest.impactWarningDetail') }}</span>
                </div>
              </div>
            </section>

            <section class="card flex min-h-0 flex-col overflow-hidden lg:h-[calc(100vh-23rem)] lg:min-h-[28rem] lg:max-h-[40rem]" data-testid="availability-models-panel">
              <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
                <div class="flex items-start justify-between gap-3">
                  <div>
                    <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.channelTest.modelsTitle') }}</h2>
                    <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.modelsHint') }}</p>
                  </div>
                  <span class="badge badge-gray whitespace-nowrap">{{ t('admin.channelTest.modelCount', { count: models.length }) }}</span>
                </div>
                <label class="relative mt-3 block">
                  <span class="sr-only">{{ t('admin.channelTest.searchModels') }}</span>
                  <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                  <input
                    v-model="modelSearch"
                    data-testid="availability-model-search"
                    type="search"
                    class="input w-full pl-9"
                    :placeholder="t('admin.channelTest.searchModels')"
                  />
                </label>
              </div>
              <div class="availability-scrollbar min-h-0 flex-1 space-y-2 overflow-y-auto p-2.5">
                <div v-if="!models.length" class="rounded-2xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  {{ loadingCatalog ? t('admin.channelTest.loading') : t('admin.channelTest.noModels') }}
                </div>
                <div v-else-if="!filteredModels.length" class="rounded-2xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  {{ t('admin.channelTest.noMatchingModels') }}
                </div>
                <button
                  v-for="model in filteredModels"
                  :key="model.id"
                  type="button"
                  class="w-full rounded-xl border p-2.5 text-left transition"
                  :class="selectedModelId === model.id
                    ? 'border-indigo-400 bg-indigo-50/70 ring-2 ring-indigo-100 dark:border-indigo-500 dark:bg-indigo-900/20 dark:ring-indigo-900/40'
                    : model.downstream_allowed === false
                      ? 'border-gray-200 bg-gray-50 opacity-70 dark:border-dark-700 dark:bg-dark-800/60'
                      : 'border-gray-200 bg-white hover:border-indigo-300 hover:bg-indigo-50/40 dark:border-dark-700 dark:bg-dark-900 dark:hover:border-indigo-700 dark:hover:bg-indigo-900/10'"
                  :data-testid="`availability-model-${model.id}`"
                  :disabled="running || sendPending || model.downstream_allowed === false"
                  :aria-pressed="selectedModelId === model.id"
                  @click="selectModel(model.id)"
                >
                  <span class="flex items-start justify-between gap-3">
                    <span class="min-w-0">
                      <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white" :title="model.id">{{ model.id }}</span>
                      <span v-if="model.upstream_model && model.upstream_model !== model.id" class="mt-1 flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                        <span>{{ t('admin.channelTest.mappedFrom') }} {{ model.id }}</span>
                        <Icon name="arrowRight" size="xs" />
                        <span class="truncate">{{ model.upstream_model }}</span>
                      </span>
                    </span>
                    <span class="badge shrink-0" :class="upstreamSupportClass(model.upstream_support)">
                      {{ upstreamSupportLabel(model.upstream_support) }}
                    </span>
                  </span>
                  <span class="mt-2 flex flex-wrap gap-1 text-[10px]">
                    <span class="badge" :class="model.downstream_allowed === true ? 'badge-success' : model.downstream_allowed === false ? 'badge-danger' : 'badge-gray'">
                      {{ downstreamLabel(model.downstream_allowed) }}
                    </span>
                    <span class="badge badge-gray">{{ model.mapping_effective ? t('admin.channelTest.mappingEffective') : t('admin.channelTest.passthrough') }}</span>
                    <span class="badge badge-gray">{{ t('admin.channelTest.modelAccounts', { count: model.account_ids.length }) }}</span>
                  </span>
                  <span class="mt-2 block border-t border-gray-100 pt-2 text-[11px] dark:border-dark-700">
                    <template v-if="model.last_test">
                      <span class="flex items-center justify-between gap-2 font-medium text-gray-700 dark:text-gray-200">
                        <span>{{ model.last_test.success ? t('common.success') : t('common.error') }} · {{ t('admin.channelTest.lastTestAt', { time: relativeTime(model.last_test.at) }) }}</span>
                        <span class="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">{{ formatMs(model.last_test.first_response_ms) }} / {{ formatMs(model.last_test.total_ms) }}</span>
                      </span>
                      <span class="mt-0.5 block truncate text-gray-500 dark:text-gray-400" :title="`${model.last_test.account_name} · ${model.last_test.model} · ${formatDateTime(model.last_test.at)}`">
                        {{ t('admin.channelTest.testedBy') }}: {{ model.last_test.account_name }} · {{ model.last_test.model }}
                      </span>
                    </template>
                    <span v-else class="block text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.lastTest') }} · {{ t('admin.channelTest.noTest') }}</span>
                  </span>
                  <span class="mt-1 block truncate text-[10px] text-gray-400 dark:text-gray-500" :title="model.source">
                    {{ t('admin.channelTest.modelSource') }}: {{ model.source || t('admin.channelTest.unknown') }}
                  </span>
                </button>
                <p class="px-1 text-[11px] leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.upstreamDirectoryNote') }}</p>
              </div>
            </section>

          <section class="card flex min-h-[28rem] min-w-0 flex-col overflow-hidden lg:col-span-2 lg:h-[calc(100vh-23rem)] lg:max-h-[40rem]" data-testid="availability-conversation-panel">
            <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700">
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <div class="flex items-center gap-2">
                    <span class="flex h-8 w-8 items-center justify-center rounded-xl bg-cyan-50 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-300">
                      <Icon name="chat" size="sm" />
                    </span>
                    <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.channelTest.conversationTitle') }}</h2>
                  </div>
                  <p class="mt-1 text-[11px] leading-4 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.contextDescription') }}</p>
                </div>
                <div class="flex flex-wrap gap-2">
                  <button type="button" data-testid="availability-new-conversation" class="btn btn-secondary btn-sm" :disabled="running || sendPending" @click="newConversation">
                    {{ t('admin.channelTest.newConversation') }}
                  </button>
                  <button type="button" data-testid="availability-reset" class="btn btn-secondary btn-sm" :disabled="running || sendPending" @click="startFreshConversation">
                    {{ t('admin.channelTest.resetConversation') }}
                  </button>
                </div>
              </div>
              <div class="mt-2 flex flex-wrap items-center gap-1.5 text-[11px]">
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.conversationFor') }}:</span>
                <span class="badge badge-gray">{{ selectedAccount ? selectedAccount.name : t('admin.channelTest.automaticAccount') }}</span>
                <span class="badge badge-gray">{{ selectedModelId || t('admin.channelTest.selectModelToChat') }}</span>
                <span v-if="conversation" class="badge badge-success">{{ t('admin.channelTest.permanent') }}</span>
                <span v-if="conversation" class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.contextRounds', { count: completedTurnCount }) }}</span>
                <span v-else class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.contextEmpty') }}</span>
              </div>
            </div>

            <div class="availability-scrollbar min-h-0 flex-1 overflow-y-auto px-4 py-3">
              <div v-if="conversationLoading" class="flex min-h-72 items-center justify-center text-sm text-gray-500 dark:text-gray-400" aria-live="polite">
                {{ t('admin.channelTest.conversationLoading') }}
              </div>
              <div v-else-if="conversation" class="space-y-5">
                <div v-if="!turns.length" class="rounded-2xl border border-dashed border-gray-300 px-5 py-12 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  {{ t('admin.channelTest.conversationNotStarted') }}
                </div>
                <div v-else class="space-y-4 pr-1" data-testid="availability-turns">
                  <article v-for="turn in turns" :key="String(turn.id)" class="space-y-3 rounded-2xl border border-gray-200 p-4 dark:border-dark-700" :data-testid="`availability-turn-${turn.id}`">
                    <div class="flex flex-wrap items-center justify-between gap-2 text-xs">
                      <div class="flex flex-wrap items-center gap-2">
                        <span class="font-semibold text-gray-700 dark:text-gray-200">#{{ turn.id }}</span>
                        <span class="badge" :class="turnStatusClass(turn.status)">{{ turnStatusLabel(turn.status) }}</span>
                        <span v-if="turnActualAttempt(turn)" class="text-gray-500 dark:text-gray-400">
                          {{ t('admin.channelTest.actualRoute') }}: {{ turnActualAttempt(turn)?.account_name }} · {{ turnActualAttempt(turn)?.model }}
                        </span>
                      </div>
                      <div class="flex flex-wrap gap-x-3 gap-y-1 text-gray-500 dark:text-gray-400">
                        <span>{{ t('admin.channelTest.firstResponse') }} {{ formatMs(turn.first_response_ms) }}</span>
                        <span>{{ t('admin.channelTest.totalTime') }} {{ formatMs(turn.total_ms) }}</span>
                      </div>
                    </div>

                    <div class="rounded-2xl bg-indigo-50/70 px-4 py-3 text-sm leading-6 text-gray-800 dark:bg-indigo-900/20 dark:text-gray-100">
                      <div class="mb-1 text-[11px] font-semibold uppercase tracking-wide text-indigo-600 dark:text-indigo-300">{{ t('admin.channelTest.operator') }}</div>
                      <div class="whitespace-pre-wrap break-words">{{ turn.prompt }}</div>
                    </div>
                    <div class="rounded-2xl bg-gray-50 px-4 py-3 text-sm leading-6 text-gray-800 dark:bg-dark-800/80 dark:text-gray-100">
                      <div class="mb-1 flex items-center justify-between gap-2 text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                        <span>{{ t('admin.channelTest.assistant') }}</span>
                        <span v-if="turn.status === 'running'">{{ t('admin.channelTest.output') }}</span>
                      </div>
                      <div v-if="turn.content" class="whitespace-pre-wrap break-words">{{ turn.content }}</div>
                      <div v-else-if="turn.status === 'running'" class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.sending') }}</div>
                      <div v-else class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.noResponse') }}</div>
                    </div>

                    <div v-if="turn.error" class="rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-xs leading-5 text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200">
                      {{ turn.error }}
                    </div>

                    <details v-if="turn.attempts.length" open class="rounded-2xl border border-gray-200 dark:border-dark-700">
                      <summary class="cursor-pointer list-none px-4 py-3 text-sm font-medium text-gray-700 dark:text-gray-200">
                        <span class="flex flex-wrap items-center justify-between gap-2">
                          <span>{{ t('admin.channelTest.attemptsTitle') }}</span>
                          <span class="badge badge-gray">{{ turn.attempts.length }}</span>
                        </span>
                      </summary>
                      <div class="space-y-2 border-t border-gray-200 p-3 dark:border-dark-700">
                        <div v-for="attempt in orderedAttempts(turn.attempts)" :key="String(attempt.id)" class="rounded-xl bg-gray-50 px-3 py-3 text-xs dark:bg-dark-800/80">
                          <div class="flex flex-wrap items-center gap-2">
                            <span class="flex h-5 min-w-5 items-center justify-center rounded-full bg-white px-1.5 font-semibold tabular-nums text-gray-600 shadow-sm dark:bg-dark-700 dark:text-gray-200">{{ attempt.index }}</span>
                            <span class="font-semibold text-gray-800 dark:text-gray-100">{{ attempt.account_name }}</span>
                            <span class="badge" :class="attemptStatusClass(attempt.status)">{{ attemptStatusLabel(attempt.status) }}</span>
                            <span v-if="attempt.status_code" class="text-gray-500 dark:text-gray-400">HTTP {{ attempt.status_code }}</span>
                          </div>
                          <div class="mt-2 grid gap-x-4 gap-y-1 text-gray-500 dark:text-gray-400 sm:grid-cols-2">
                            <span>{{ t('admin.channelTest.requestedModel') }}: {{ attempt.requested_model }}</span>
                            <span>{{ t('admin.channelTest.upstreamModel') }}: {{ attempt.model }}</span>
                            <span>{{ t('admin.channelTest.startedAt') }}: {{ formatDateTime(attempt.started_at) }}</span>
                            <span>{{ t('admin.channelTest.firstResponse') }}: {{ formatMs(attempt.first_response_ms) }} · {{ t('admin.channelTest.totalTime') }}: {{ formatMs(attempt.total_ms) }}</span>
                          </div>
                          <code v-if="attempt.endpoint" class="mt-2 block break-all rounded-lg bg-white px-2 py-1.5 font-mono text-[11px] text-gray-600 dark:bg-dark-900 dark:text-gray-300">{{ attempt.endpoint }}</code>
                          <div v-if="attempt.reason || attempt.error" class="mt-2 break-words text-red-600 dark:text-red-300">
                            <span v-if="attempt.reason">{{ t('admin.channelTest.reason') }}: {{ attempt.reason }}</span>
                            <span v-if="attempt.reason && attempt.error"> · </span>
                            <span v-if="attempt.error">{{ t('admin.channelTest.error') }}: {{ attempt.error }}</span>
                          </div>
                        </div>
                      </div>
                    </details>
                    <div v-else class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.noAttempts') }}</div>
                  </article>
                </div>
                <button v-if="hasMoreTurns" type="button" class="btn btn-secondary w-full" :disabled="loadingMoreTurns" @click="loadMoreTurns">
                  {{ loadingMoreTurns ? t('admin.channelTest.loading') : t('admin.channelTest.loadMoreTurns') }}
                </button>
              </div>
              <div v-else class="flex min-h-72 items-center justify-center rounded-2xl border border-dashed border-gray-300 px-6 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
                {{ t('admin.channelTest.conversationNotStarted') }}
              </div>

              <div v-if="liveEvents.length" class="mt-5 rounded-2xl border border-cyan-200 bg-cyan-50/50 p-4 dark:border-cyan-900/50 dark:bg-cyan-900/10" data-testid="availability-events">
                <div class="flex items-center justify-between gap-3">
                  <h3 class="text-sm font-semibold text-cyan-950 dark:text-cyan-100">{{ t('admin.channelTest.eventTimeline') }}</h3>
                  <span v-if="streamStatus !== 'idle'" class="badge" :class="streamStatusClass(streamStatus)">{{ streamStatusLabel(streamStatus) }}</span>
                </div>
                <ol class="availability-scrollbar mt-3 max-h-52 space-y-2 overflow-y-auto pr-1" aria-live="polite">
                  <li v-for="event in liveEvents" :key="`${event.seq}-${event.type}`" class="flex items-start gap-2 text-xs leading-5 text-cyan-950 dark:text-cyan-100">
                    <span class="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-cyan-500"></span>
                    <span class="min-w-0">
                      <span class="font-semibold">{{ eventLabel(event.type) }}</span>
                      <span v-if="event.attempt" class="text-cyan-800 dark:text-cyan-200">
                        · {{ event.attempt.account_name }} · {{ event.attempt.model }}
                        <span v-if="event.attempt.index > 1"> · {{ t('admin.channelTest.retryAttempt') }}</span>
                      </span>
                      <span v-if="event.attempt?.endpoint" class="text-cyan-700 dark:text-cyan-300"> · {{ event.attempt.endpoint }}</span>
                      <span v-if="event.attempt?.reason || event.attempt?.error || event.error" class="block break-words text-red-700 dark:text-red-300">
                        {{ event.attempt?.reason || event.attempt?.error || event.error }}
                      </span>
                    </span>
                  </li>
                </ol>
              </div>

              <div v-if="streamError" class="mt-5 rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm leading-6 text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200" role="alert">
                <div class="flex flex-wrap items-start justify-between gap-3">
                  <div class="flex min-w-0 items-start gap-2">
                    <Icon name="exclamationCircle" size="sm" class="mt-0.5 shrink-0" />
                    <span class="break-words">{{ streamError }}</span>
                  </div>
                  <div v-if="streamStatus === 'disconnected'" class="flex shrink-0 flex-wrap gap-3 text-xs font-semibold">
                    <button
                      v-if="pendingStreamRequest"
                      type="button"
                      data-testid="availability-reconnect"
                      class="underline underline-offset-2"
                      :disabled="running || conversationLoading"
                      @click="resumeTurn"
                    >
                      {{ t('admin.channelTest.reconnect') }}
                    </button>
                    <button type="button" class="underline underline-offset-2" :disabled="conversationLoading" @click="refreshConversationStatus">
                      {{ t('admin.channelTest.refreshStatus') }}
                    </button>
                  </div>
                </div>
                <p v-if="streamStatus === 'disconnected'" class="mt-2 pl-6 text-xs leading-5">{{ t('admin.channelTest.disconnectedHint') }}</p>
              </div>
            </div>

            <form class="border-t border-gray-200 bg-gray-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/30" @submit.prevent="sendTurn">
              <label for="availability-prompt" class="input-label">{{ t('admin.channelTest.prompt') }}</label>
              <textarea
                id="availability-prompt"
                v-model="prompt"
                data-testid="availability-prompt"
                rows="2"
                class="input mt-1.5 w-full resize-y leading-5"
                :placeholder="t('admin.channelTest.promptPlaceholder')"
                :disabled="running || sendPending"
              ></textarea>
              <div class="mt-2 flex flex-wrap items-center justify-between gap-2">
                <span v-if="!selectedModelId" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.channelTest.selectModelToChat') }}</span>
                <span v-else-if="selectedModel?.downstream_allowed === false" class="text-xs text-red-600 dark:text-red-300">{{ t('admin.channelTest.downstreamClosed') }}</span>
                <span v-else class="text-xs text-gray-500 dark:text-gray-400">{{ selectedAccount ? t('admin.channelTest.explicitAccountHint') : t('admin.channelTest.autoRouteHint') }}</span>
                <div class="flex flex-wrap gap-2">
                  <button v-if="running" type="button" data-testid="availability-cancel" class="btn btn-secondary" @click="cancelTurn">
                    <Icon name="x" size="sm" class="mr-1" />{{ t('admin.channelTest.stop') }}
                  </button>
                  <button type="submit" data-testid="availability-send" class="btn btn-primary" :disabled="!canSend">
                    <Icon v-if="running" name="refresh" size="sm" class="mr-1 animate-spin" />
                    <Icon v-else name="arrowRight" size="sm" class="mr-1" />
                    {{ running ? t('admin.channelTest.sending') : t('admin.channelTest.send') }}
                  </button>
                </div>
              </div>
            </form>
          </section>
        </div>
      </template>

      <details class="card overflow-hidden" data-testid="availability-history-panel">
          <summary class="cursor-pointer list-none px-4 py-3 marker:hidden">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div class="flex items-center gap-2">
                  <span class="flex h-8 w-8 items-center justify-center rounded-xl bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
                    <Icon name="database" size="sm" />
                  </span>
                  <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.channelTest.historyTitle') }}</h2>
                  <span class="badge badge-success">{{ t('admin.channelTest.permanent') }}</span>
                </div>
                <p class="mt-1 text-[11px] leading-4 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.historyHint') }}</p>
              </div>
              <span class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.channelTest.totalConversations', { count: historyTotal }) }}
                <Icon name="chevronDown" size="sm" class="history-chevron transition-transform" />
              </span>
            </div>
          </summary>
          <div class="border-t border-gray-200 px-4 py-3 dark:border-dark-700">
            <label class="relative block max-w-xl">
              <span class="sr-only">{{ t('admin.channelTest.searchHistory') }}</span>
              <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                v-model="historySearch"
                data-testid="availability-history-search"
                type="search"
                class="input w-full pl-9"
                :placeholder="t('admin.channelTest.searchHistory')"
              />
            </label>
          </div>

          <div v-if="historyError" class="mx-5 mt-4 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-200 sm:mx-6" role="alert">
            {{ historyError }}
          </div>
          <div v-if="loadingHistory" class="px-5 py-10 text-center text-sm text-gray-500 dark:text-gray-400" aria-live="polite">{{ t('admin.channelTest.loading') }}</div>
          <div v-else-if="historyItems.length" class="p-3 sm:p-5">
            <div class="hidden grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] gap-4 px-3 pb-2 text-[11px] font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500 md:grid">
              <span>{{ t('admin.channelTest.historyGroup') }}</span>
              <span>{{ t('admin.channelTest.historyAccount') }}</span>
              <span>{{ t('admin.channelTest.selectedModel') }}</span>
              <span>{{ t('admin.channelTest.time') }}</span>
              <span></span>
            </div>
            <div class="space-y-2">
              <div v-for="item in historyItems" :key="item.id" class="grid gap-3 rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] md:items-center md:gap-4 md:p-3">
                <div class="min-w-0">
                  <div class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ historyGroupName(item.group_id) }}</div>
                  <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">#{{ item.id }} · {{ formatDateTime(item.created_at) }}</div>
                </div>
                <div class="min-w-0 text-sm text-gray-700 dark:text-gray-200">
                  <span class="text-[11px] text-gray-400 md:hidden">{{ t('admin.channelTest.historyAccount') }} · </span>{{ historyAccountName(item) }}
                </div>
                <div class="min-w-0 truncate text-sm text-gray-700 dark:text-gray-200">
                  <span class="text-[11px] text-gray-400 md:hidden">{{ t('admin.channelTest.selectedModel') }} · </span>{{ item.model }}
                </div>
                <div class="text-sm text-gray-500 dark:text-gray-400">
                  <span class="text-[11px] text-gray-400 md:hidden">{{ t('admin.channelTest.time') }} · </span><span :title="formatDateTime(item.updated_at)">{{ relativeTime(item.updated_at) }}</span>
                </div>
                <button type="button" :data-testid="`availability-history-restore-${item.id}`" class="btn btn-secondary btn-sm justify-self-start md:justify-self-end" :disabled="running || sendPending || conversationLoading" @click="restoreConversation(item)">
                  {{ t('admin.channelTest.restore') }}
                </button>
              </div>
            </div>
          </div>
          <div v-else class="px-5 py-10 text-center text-sm text-gray-500 dark:text-gray-400">
            {{ historySearch.trim() ? t('admin.channelTest.noHistoryMatch') : t('admin.channelTest.noHistory') }}
          </div>
          <div v-if="historyPages > 1" class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 px-5 py-4 text-sm dark:border-dark-700 sm:px-6">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage <= 1 || loadingHistory" @click="changeHistoryPage(-1)">
              <Icon name="chevronLeft" size="sm" class="mr-1" />{{ t('admin.channelTest.previous') }}
            </button>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.pageOf', { page: historyPage, pages: historyPages }) }}</span>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="historyPage >= historyPages || loadingHistory" @click="changeHistoryPage(1)">
              {{ t('admin.channelTest.next') }}<Icon name="chevronRight" size="sm" class="ml-1" />
            </button>
          </div>
      </details>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { adminAPI } from '@/api/admin'
import type {
  AvailabilityAccount,
  AvailabilityAttempt,
  AvailabilityCatalog,
  AvailabilityConversation,
  AvailabilityConversationDetail,
  AvailabilityEvent,
  AvailabilityEventType,
  AvailabilityModel,
  AvailabilityStatus,
  AvailabilityTurn,
  TurnStatus
} from '@/api/admin/availability'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import type { AdminGroup, UpdateAccountRequest } from '@/types'

type AccountField = 'priority' | 'load_factor'
type AccountFieldState = 'idle' | 'saving' | 'saved' | 'error'
type StreamStatus = 'idle' | 'routing' | 'queued' | 'waiting' | 'sent' | 'output' | 'completed' | 'cancelled' | 'failed' | 'disconnected'

const { t } = useI18n()
const appStore = useAppStore()

const groups = ref<AdminGroup[]>([])
const selectedGroupId = ref<number | null>(null)
const selectedAccountId = ref<number | null>(null)
const selectedModelId = ref('')
const catalog = ref<AvailabilityCatalog | null>(null)
const groupAccounts = ref<AvailabilityAccount[]>([])
const accountSearch = ref('')
const modelSearch = ref('')
const loadingGroups = ref(false)
const loadingCatalog = ref(false)
const pageError = ref('')

const conversation = ref<AvailabilityConversation | null>(null)
const turns = ref<AvailabilityTurn[]>([])
const conversationLoading = ref(false)
const loadingMoreTurns = ref(false)
const conversationTurnTotal = ref(0)
const conversationTurnPage = ref(1)
const prompt = ref('')
const liveEvents = ref<AvailabilityEvent[]>([])
const streamError = ref('')
const streamStatus = ref<StreamStatus>('idle')
const running = ref(false)
const sendPending = ref(false)
const lastStreamSeq = ref<number | null>(null)

interface PendingStreamRequest {
  conversationId: number
  prompt: string
  clientRequestId: string
}

const pendingStreamRequest = ref<PendingStreamRequest | null>(null)

const historyItems = ref<AvailabilityConversation[]>([])
const historyTotal = ref(0)
const historyPage = ref(1)
const historyPageSize = 20
const historySearch = ref('')
const loadingHistory = ref(false)
const historyError = ref('')
const loadingConversationId = ref<number | null>(null)

const accountDrafts = reactive<Record<string, string>>({})
const accountSaving = reactive(new Set<string>())
const accountSaveStates = reactive<Record<string, AccountFieldState>>({})
const accountSaveErrors = reactive<Record<string, string>>({})

let catalogController: AbortController | null = null
let historyController: AbortController | null = null
let streamController: AbortController | null = null
let catalogRequestVersion = 0
let historyRequestVersion = 0
let streamRunVersion = 0
let restoreRequestVersion = 0
let historySearchTimer: ReturnType<typeof setTimeout> | null = null
let isBootstrapping = true
let isApplyingRestoredScope = false

const selectedGroup = computed(() => groups.value.find(group => group.id === selectedGroupId.value) ?? null)
const selectedAccount = computed(() => accounts.value.find(account => account.id === selectedAccountId.value) ?? null)
const selectedModel = computed(() => models.value.find(model => model.id === selectedModelId.value) ?? null)
const accounts = computed(() => {
  const source = groupAccounts.value.length ? groupAccounts.value : (catalog.value?.accounts ?? [])
  return source
    .map((account, index) => ({ account, index }))
    .sort((left, right) => left.account.rank - right.account.rank || left.index - right.index)
    .map(({ account }) => account)
})
const models = computed(() => catalog.value?.models ?? [])
const filteredAccounts = computed(() => {
  const query = accountSearch.value.trim().toLocaleLowerCase()
  if (!query) return accounts.value
  return accounts.value.filter(account => `${account.name} ${account.platform} ${account.status} ${account.id}`.toLocaleLowerCase().includes(query))
})
const filteredModels = computed(() => {
  const query = modelSearch.value.trim().toLocaleLowerCase()
  if (!query) return models.value
  return models.value.filter(model => `${model.id} ${model.upstream_model || ''} ${model.source}`.toLocaleLowerCase().includes(query))
})
const completedTurnCount = computed(() => turns.value.filter(turn => turn.status === 'succeeded').length)
const hasMoreTurns = computed(() => conversation.value != null && conversationTurnTotal.value > turns.value.length)
const historyPages = computed(() => Math.max(1, Math.ceil(historyTotal.value / historyPageSize)))
const canSend = computed(() => (
  !running.value &&
  !sendPending.value &&
  selectedGroupId.value != null &&
  selectedModelId.value.trim().length > 0 &&
  selectedModel.value != null &&
  selectedModel.value?.downstream_allowed !== false
))

function errorText(error: unknown, fallback = t('admin.channelTest.requestFailed')): string {
  if (typeof error === 'string' && error.trim()) return error
  if (error && typeof error === 'object') {
    const candidate = error as { message?: unknown; error?: unknown; reason?: unknown }
    for (const value of [candidate.message, candidate.error, candidate.reason]) {
      if (typeof value === 'string' && value.trim()) return value
      if (value && typeof value === 'object' && 'message' in value) {
        const nested = (value as { message?: unknown }).message
        if (typeof nested === 'string' && nested.trim()) return nested
      }
    }
  }
  return fallback
}

function isAbortError(error: unknown): boolean {
  return Boolean(error && typeof error === 'object' && 'name' in error && (error as { name?: unknown }).name === 'AbortError')
}

async function loadGroups(): Promise<void> {
  loadingGroups.value = true
  try {
    const loaded = await adminAPI.groups.getAll()
    groups.value = loaded
    if (!selectedGroupId.value || !loaded.some(group => group.id === selectedGroupId.value)) {
      selectedGroupId.value = loaded[0]?.id ?? null
    }
  } catch (error) {
    pageError.value = errorText(error, t('admin.channelTest.catalogFailed'))
    appStore.showError(pageError.value)
  } finally {
    loadingGroups.value = false
  }
}

async function loadCatalog(
  accountId: number | null = selectedAccountId.value,
  options: { preserveModel?: boolean; silent?: boolean } = {}
): Promise<boolean> {
  const groupId = selectedGroupId.value
  if (groupId == null) {
    catalog.value = null
    return false
  }

  catalogController?.abort()
  const controller = new AbortController()
  catalogController = controller
  const requestVersion = ++catalogRequestVersion
  loadingCatalog.value = true

  try {
    const loaded = await adminAPI.availability.getCatalog(groupId, accountId, { signal: controller.signal })
    if (requestVersion !== catalogRequestVersion || selectedGroupId.value !== groupId) return false
    const loadedAccount = accountId == null
      ? null
      : loaded.accounts.find(account => account.id === accountId) ?? null
    if (accountId != null && !loadedAccount) {
      if (selectedAccountId.value === accountId) {
        selectedAccountId.value = null
        resetConversation()
        appStore.showError(t('admin.channelTest.accountSelectionRejected'))
      }
      return false
    }

    catalog.value = loaded
    if (accountId == null) {
      groupAccounts.value = loaded.accounts
    } else if (loadedAccount) {
      const existingIndex = groupAccounts.value.findIndex(account => account.id === accountId)
      if (existingIndex >= 0) {
        const existing = groupAccounts.value[existingIndex]
        groupAccounts.value[existingIndex] = { ...existing, ...loadedAccount, rank: existing.rank }
      } else if (!groupAccounts.value.length) {
        // A conversation restored directly from history may not have loaded the
        // group catalog yet; the account-scoped response is still safe to show.
        groupAccounts.value = loaded.accounts
      }
    }
    if (selectedModelId.value && !loaded.models.some(model => model.id === selectedModelId.value)) {
      selectedModelId.value = ''
    }
    pageError.value = ''
    return true
  } catch (error) {
    if (isAbortError(error) || controller.signal.aborted) return false
    if (requestVersion === catalogRequestVersion) {
      pageError.value = errorText(error, t('admin.channelTest.catalogFailed'))
      if (!options.silent) appStore.showError(pageError.value)
    }
    return false
  } finally {
    if (requestVersion === catalogRequestVersion) loadingCatalog.value = false
  }
}

async function refreshAll(): Promise<void> {
  pageError.value = ''
  await loadGroups()
  if (selectedGroupId.value != null) {
    await loadCatalog(selectedAccountId.value, { preserveModel: true })
  }
  await loadHistory({ silent: true })
}

function resetConversation(): void {
  if (running.value) return
  pendingStreamRequest.value = null
  conversation.value = null
  turns.value = []
  conversationTurnTotal.value = 0
  conversationTurnPage.value = 1
  liveEvents.value = []
  streamError.value = ''
  streamStatus.value = 'idle'
  lastStreamSeq.value = null
}

function startFreshConversation(): void {
  restoreRequestVersion += 1
  resetConversation()
}

function newConversation(): void {
  startFreshConversation()
}

async function handleGroupChange(): Promise<void> {
  if (isBootstrapping || isApplyingRestoredScope) return
  restoreRequestVersion += 1
  selectedAccountId.value = null
  selectedModelId.value = ''
  accountSearch.value = ''
  modelSearch.value = ''
  resetConversation()
  pageError.value = ''
  catalog.value = null
  groupAccounts.value = []
  if (selectedGroupId.value != null) await loadCatalog(null)
}

async function selectAutomaticAccount(): Promise<void> {
  if (running.value || selectedAccountId.value == null) return
  selectedAccountId.value = null
  restoreRequestVersion += 1
  resetConversation()
  await loadCatalog(null, { preserveModel: true })
}

async function selectAccount(account: AvailabilityAccount): Promise<void> {
  if (running.value || selectedAccountId.value === account.id) return
  const previousAccountId = selectedAccountId.value
  selectedAccountId.value = account.id
  restoreRequestVersion += 1
  resetConversation()
  const accepted = await loadCatalog(account.id, { preserveModel: true })
  if (!accepted && selectedAccountId.value === account.id) {
    selectedAccountId.value = previousAccountId
    resetConversation()
  }
}

function selectModel(modelId: string): void {
  if (running.value || selectedModelId.value === modelId) return
  selectedModelId.value = modelId
}

function accountFieldKey(accountId: number, field: AccountField): string {
  return `${accountId}:${field}`
}

function accountFieldValue(account: AvailabilityAccount, field: AccountField): string {
  const key = accountFieldKey(account.id, field)
  if (Object.prototype.hasOwnProperty.call(accountDrafts, key)) return accountDrafts[key]
  if (field === 'priority') return String(account.priority ?? '')
  return account.load_factor == null ? '' : String(account.load_factor)
}

function updateAccountDraft(account: AvailabilityAccount, field: AccountField, event: Event): void {
  const input = event.target as HTMLInputElement | null
  if (!input) return
  const key = accountFieldKey(account.id, field)
  accountDrafts[key] = input.value
  accountSaveStates[key] = 'idle'
  delete accountSaveErrors[key]
}

function cancelAccountField(account: AvailabilityAccount, field: AccountField): void {
  const key = accountFieldKey(account.id, field)
  delete accountDrafts[key]
  delete accountSaveErrors[key]
  accountSaveStates[key] = 'idle'
}

function accountFieldState(accountId: number, field: AccountField): AccountFieldState {
  return accountSaveStates[accountFieldKey(accountId, field)] ?? 'idle'
}

function isAccountFieldSaving(accountId: number, field: AccountField): boolean {
  return accountSaving.has(accountFieldKey(accountId, field))
}

function accountFieldError(accountId: number, field: AccountField): string {
  return accountSaveErrors[accountFieldKey(accountId, field)] ?? ''
}

function normalizeAccountField(field: AccountField, rawValue: string): number | null {
  const value = rawValue.trim()
  if (field === 'load_factor' && value === '') return 0
  if (value === '') return null
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < 0) return null
  if (field === 'load_factor' && parsed > 10000) return null
  return parsed
}

function currentAccountFieldValue(account: AvailabilityAccount, field: AccountField): number {
  if (field === 'priority') return Number(account.priority ?? 0)
  return account.load_factor == null ? 0 : Number(account.load_factor)
}

async function commitAccountField(account: AvailabilityAccount, field: AccountField): Promise<void> {
  const key = accountFieldKey(account.id, field)
  if (accountSaving.has(key)) return
  const rawValue = Object.prototype.hasOwnProperty.call(accountDrafts, key)
    ? accountDrafts[key]
    : accountFieldValue(account, field)
  const value = normalizeAccountField(field, rawValue)

  if (value == null) {
    delete accountDrafts[key]
    accountSaveStates[key] = 'error'
    accountSaveErrors[key] = t('common.invalidNumber')
    appStore.showError(accountSaveErrors[key])
    return
  }
  if (value === currentAccountFieldValue(account, field)) {
    cancelAccountField(account, field)
    return
  }

  accountSaving.add(key)
  accountSaveStates[key] = 'saving'
  delete accountSaveErrors[key]
  try {
    const updates: UpdateAccountRequest = field === 'priority' ? { priority: value } : { load_factor: value }
    const updated = await adminAPI.accounts.update(account.id, updates)
    const current = catalog.value?.accounts.find(item => item.id === account.id)
    if (current) {
      if (field === 'priority') current.priority = Number(updated.priority ?? value)
      else current.load_factor = updated.load_factor === undefined ? value : (updated.load_factor ?? null)
    }
    delete accountDrafts[key]
    accountSaveStates[key] = 'saved'
    await loadCatalog(selectedAccountId.value, { preserveModel: true, silent: true })
  } catch (error) {
    // The draft is discarded so the visible value rolls back to the last server value.
    delete accountDrafts[key]
    accountSaveStates[key] = 'error'
    accountSaveErrors[key] = errorText(error, t('admin.channelTest.saveFailed'))
    appStore.showError(accountSaveErrors[key])
  } finally {
    accountSaving.delete(key)
  }
}

function formatConcurrency(account: AvailabilityAccount): string {
  const current = account.current_concurrency
  const limit = Number.isFinite(account.concurrency) ? account.concurrency : null
  if (current != null && limit != null) return `${current}/${limit}`
  if (limit != null) return String(limit)
  return '—'
}

function formatMs(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '—'
  const rounded = Math.max(0, Math.round(value))
  return rounded < 1000 ? `${rounded} ms` : `${(rounded / 1000).toFixed(2)} s`
}

function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'medium' }).format(date)
}

function relativeTime(value: string | null | undefined): string {
  if (!value) return '—'
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value
  let difference = (timestamp - Date.now()) / 1000
  const absolute = Math.abs(difference)
  let divisor = 1
  let unit: Intl.RelativeTimeFormatUnit = 'second'
  if (absolute >= 86400) {
    divisor = 86400
    unit = 'day'
  } else if (absolute >= 3600) {
    divisor = 3600
    unit = 'hour'
  } else if (absolute >= 60) {
    divisor = 60
    unit = 'minute'
  }
  difference = Math.round(difference / divisor)
  return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(difference, unit)
}

function upstreamSupportLabel(value: AvailabilityModel['upstream_support']): string {
  if (value === 'declared') return t('admin.channelTest.declared')
  if (value === 'unsupported') return t('admin.channelTest.unsupported')
  return t('admin.channelTest.unknown')
}

function upstreamSupportClass(value: AvailabilityModel['upstream_support']): string {
  if (value === 'declared') return 'badge-success'
  if (value === 'unsupported') return 'badge-danger'
  return 'badge-gray'
}

function downstreamLabel(value: boolean | null): string {
  if (value === true) return t('admin.channelTest.downstreamOpen')
  if (value === false) return t('admin.channelTest.downstreamClosed')
  return t('admin.channelTest.downstreamUnknown')
}

function turnStatusClass(status: TurnStatus): string {
  if (status === 'succeeded') return 'badge-success'
  if (status === 'failed' || status === 'cancelled') return 'badge-danger'
  return 'badge-gray'
}

function turnStatusLabel(status: TurnStatus): string {
  if (status === 'succeeded') return t('admin.channelTest.completed')
  if (status === 'failed') return t('admin.channelTest.failed')
  if (status === 'cancelled') return t('admin.channelTest.cancelled')
  return t('admin.channelTest.running')
}

function attemptStatusClass(status: AvailabilityStatus): string {
  if (status === 'succeeded') return 'badge-success'
  if (status === 'failed' || status === 'cancelled') return 'badge-danger'
  return 'badge-gray'
}

function attemptStatusLabel(status: AvailabilityStatus): string {
  if (status === 'succeeded') return t('admin.channelTest.completed')
  if (status === 'failed') return t('admin.channelTest.failed')
  if (status === 'cancelled') return t('admin.channelTest.cancelled')
  return t('admin.channelTest.running')
}

function streamStatusClass(status: StreamStatus): string {
  if (status === 'completed') return 'badge-success'
  if (status === 'failed' || status === 'cancelled' || status === 'disconnected') return 'badge-danger'
  return 'badge-gray'
}

function streamStatusLabel(status: StreamStatus): string {
  if (status === 'routing') return t('admin.channelTest.routing')
  if (status === 'queued') return t('admin.channelTest.queued')
  if (status === 'waiting') return t('admin.channelTest.waitingFirst')
  if (status === 'sent') return t('admin.channelTest.sent')
  if (status === 'output') return t('admin.channelTest.output')
  if (status === 'completed') return t('admin.channelTest.completed')
  if (status === 'cancelled') return t('admin.channelTest.cancelled')
  if (status === 'failed') return t('admin.channelTest.failed')
  if (status === 'disconnected') return t('admin.channelTest.disconnected')
  return t('admin.channelTest.running')
}

function eventLabel(type: AvailabilityEventType): string {
  if (type === 'turn_started') return t('admin.channelTest.turnStarted')
  if (type === 'routing') return t('admin.channelTest.routing')
  if (type === 'attempt_started') return t('admin.channelTest.attemptStarted')
  if (type === 'attempt_failed') return t('admin.channelTest.attemptFailedEvent')
  if (type === 'content_delta') return t('admin.channelTest.contentDelta')
  if (type === 'turn_completed') return t('admin.channelTest.turnCompleted')
  if (type === 'turn_failed') return t('admin.channelTest.turnFailed')
  return t('admin.channelTest.turnCancelled')
}

function orderedAttempts(attempts: AvailabilityAttempt[]): AvailabilityAttempt[] {
  return [...attempts].sort((left, right) => left.index - right.index)
}

function turnActualAttempt(turn: AvailabilityTurn): AvailabilityAttempt | null {
  const ordered = orderedAttempts(turn.attempts)
  return [...ordered].reverse().find(attempt => attempt.status === 'succeeded') ?? [...ordered].reverse()[0] ?? null
}

function mergeAttempts(existing: AvailabilityAttempt[], incoming: AvailabilityAttempt[]): AvailabilityAttempt[] {
  const byId = new Map<string, AvailabilityAttempt>()
  for (const attempt of existing) byId.set(String(attempt.id), attempt)
  for (const attempt of incoming) {
    const key = String(attempt.id)
    const previous = byId.get(key)
    byId.set(key, previous ? { ...previous, ...attempt } : attempt)
  }
  return orderedAttempts([...byId.values()])
}

function mergeTurns(existing: AvailabilityTurn, incoming: AvailabilityTurn): AvailabilityTurn {
  return {
    ...existing,
    ...incoming,
    prompt: typeof incoming.prompt === 'string' ? incoming.prompt : existing.prompt,
    content: typeof incoming.content === 'string' ? incoming.content : existing.content,
    attempts: mergeAttempts(existing.attempts ?? [], incoming.attempts ?? [])
  }
}

function sortTurns(items: AvailabilityTurn[]): AvailabilityTurn[] {
  return [...items].sort((left, right) => {
    const leftTime = Date.parse(left.created_at)
    const rightTime = Date.parse(right.created_at)
    if (Number.isFinite(leftTime) && Number.isFinite(rightTime) && leftTime !== rightTime) return leftTime - rightTime
    return String(left.id).localeCompare(String(right.id))
  })
}

function upsertTurn(turn: AvailabilityTurn): void {
  const index = turns.value.findIndex(item => String(item.id) === String(turn.id))
  if (index < 0) turns.value = sortTurns([...turns.value, turn])
  else turns.value[index] = mergeTurns(turns.value[index], turn)
}

function findActiveTurn(): AvailabilityTurn | null {
  if (activeTurnId.value == null) return null
  return turns.value.find(turn => String(turn.id) === String(activeTurnId.value)) ?? null
}

const activeTurnId = ref<number | string | null>(null)
const activeTurnPrompt = ref('')

function patchActiveTurn(patch: Partial<AvailabilityTurn>): void {
  const current = findActiveTurn()
  if (!current) return
  const index = turns.value.findIndex(turn => String(turn.id) === String(current.id))
  if (index >= 0) turns.value[index] = { ...current, ...patch, attempts: current.attempts }
}

function resolveEventTurn(event: AvailabilityEvent): AvailabilityTurn {
  const existing = turns.value.find(turn => String(turn.id) === String(event.turn_id))
  if (existing) {
    activeTurnId.value = existing.id
    return existing
  }

  const pendingIndex = activeTurnId.value == null
    ? -1
    : turns.value.findIndex(turn => String(turn.id) === String(activeTurnId.value))
  if (pendingIndex >= 0) {
    const pending = turns.value[pendingIndex]
    const replaced = { ...pending, id: event.turn_id }
    turns.value[pendingIndex] = replaced
    activeTurnId.value = event.turn_id
    return replaced
  }

  const created: AvailabilityTurn = {
    id: event.turn_id,
    conversation_id: event.conversation_id,
    prompt: activeTurnPrompt.value,
    content: '',
    status: 'running',
    created_at: event.at,
    completed_at: null,
    first_response_ms: null,
    total_ms: null,
    attempts: []
  }
  turns.value = sortTurns([...turns.value, created])
  activeTurnId.value = created.id
  return created
}

function handleStreamEvent(event: AvailabilityEvent): void {
  if (!conversation.value || event.conversation_id !== conversation.value.id) return
  if (lastStreamSeq.value != null && event.seq <= lastStreamSeq.value) return
  lastStreamSeq.value = event.seq
  liveEvents.value.push(event)

  const turn = resolveEventTurn(event)
  if (event.turn) {
    upsertTurn(event.turn)
  }
  if (event.attempt) {
    const current = findActiveTurn() ?? turn
    const index = turns.value.findIndex(item => String(item.id) === String(current.id))
    if (index >= 0) {
      turns.value[index] = {
        ...current,
        attempts: mergeAttempts(current.attempts ?? [], [event.attempt])
      }
    }
  }
  if (event.delta) {
    const current = findActiveTurn() ?? turn
    patchActiveTurn({ content: `${current.content || ''}${event.delta}`, status: 'running' })
  }

  if (event.type === 'turn_started') streamStatus.value = 'routing'
  else if (event.type === 'routing') streamStatus.value = 'queued'
  else if (event.type === 'attempt_started') streamStatus.value = 'waiting'
  else if (event.type === 'attempt_failed') streamStatus.value = 'routing'
  else if (event.type === 'content_delta') streamStatus.value = 'output'
  else if (event.type === 'turn_completed') {
    streamStatus.value = 'completed'
    patchActiveTurn({ status: 'succeeded', error: undefined })
  } else if (event.type === 'turn_failed') {
    streamStatus.value = 'failed'
    streamError.value = event.error || event.turn?.error || t('admin.channelTest.requestFailed')
    patchActiveTurn({ status: 'failed', error: streamError.value })
  } else if (event.type === 'turn_cancelled') {
    streamStatus.value = 'cancelled'
    patchActiveTurn({ status: 'cancelled' })
  }
}

function newClientRequestId(): string {
  try {
    return globalThis.crypto?.randomUUID?.() ?? `availability-${Date.now()}-${Math.random().toString(36).slice(2)}`
  } catch {
    return `availability-${Date.now()}-${Math.random().toString(36).slice(2)}`
  }
}

function conversationScopeMatches(value: AvailabilityConversation): boolean {
  return value.group_id === selectedGroupId.value && value.account_id === selectedAccountId.value && value.model === selectedModelId.value
}

async function ensureConversation(firstPrompt: string): Promise<AvailabilityConversation> {
  if (conversation.value && conversationScopeMatches(conversation.value)) return conversation.value
  if (conversation.value) resetConversation()
  if (selectedGroupId.value == null || !selectedModelId.value) throw new Error(t('admin.channelTest.conversationFailed'))
  const payload: { group_id: number; account_id?: number; model: string; title?: string } = {
    group_id: selectedGroupId.value,
    model: selectedModelId.value,
    title: firstPrompt.slice(0, 80)
  }
  if (selectedAccountId.value != null) payload.account_id = selectedAccountId.value
  const created = await adminAPI.availability.createConversation(payload)
  conversation.value = created
  turns.value = []
  conversationTurnTotal.value = 0
  conversationTurnPage.value = 1
  return created
}

function makeLocalTurn(conversationId: number, text: string): AvailabilityTurn {
  return {
    id: `local-${newClientRequestId()}`,
    conversation_id: conversationId,
    prompt: text,
    content: '',
    status: 'running',
    created_at: new Date().toISOString(),
    completed_at: null,
    first_response_ms: null,
    total_ms: null,
    attempts: []
  }
}

async function runStreamRequest(request: PendingStreamRequest): Promise<void> {
  const controller = new AbortController()
  streamController = controller
  const runVersion = ++streamRunVersion
  running.value = true
  let terminalEventReceived = false
  try {
    const result = await adminAPI.availability.streamTurn(
      request.conversationId,
      { prompt: request.prompt, client_request_id: request.clientRequestId },
      {
        signal: controller.signal,
        onEvent: event => {
          if (runVersion !== streamRunVersion) return
          if (event.type === 'turn_completed' || event.type === 'turn_failed' || event.type === 'turn_cancelled') terminalEventReceived = true
          handleStreamEvent(event)
        }
      }
    )
    if (runVersion !== streamRunVersion) return
    terminalEventReceived ||= result.receivedTerminalEvent
    if (!terminalEventReceived) {
      streamStatus.value = 'disconnected'
      streamError.value = t('admin.channelTest.disconnected')
      await refreshActiveConversation({ silent: true })
    } else if ((streamStatus.value as StreamStatus) === 'failed' || (streamStatus.value as StreamStatus) === 'cancelled') {
      prompt.value = request.prompt
      pendingStreamRequest.value = null
    }
  } catch (error) {
    if (runVersion !== streamRunVersion) return
    if ((isAbortError(error) || controller.signal.aborted) && !terminalEventReceived) {
      streamStatus.value = 'cancelled'
      patchActiveTurn({ status: 'cancelled', completed_at: new Date().toISOString() })
      pendingStreamRequest.value = null
      await refreshActiveConversation({ silent: true })
      if (findActiveTurn()?.status === 'running') patchActiveTurn({ status: 'cancelled', completed_at: new Date().toISOString() })
    } else if (!terminalEventReceived) {
      const status = error && typeof error === 'object' && 'status' in error
        ? Number((error as { status?: unknown }).status)
        : 0
      const disconnected = !Number.isFinite(status) || status <= 0
      streamStatus.value = disconnected ? 'disconnected' : 'failed'
      streamError.value = disconnected ? t('admin.channelTest.disconnected') : errorText(error)
      patchActiveTurn({ status: disconnected ? 'running' : 'failed', error: disconnected ? undefined : streamError.value })
      if (!disconnected) {
        prompt.value = request.prompt
        pendingStreamRequest.value = null
      }
      await refreshActiveConversation({ silent: true })
    }
  } finally {
    if (runVersion === streamRunVersion) {
      running.value = false
      if (streamController === controller) streamController = null
      void loadHistory({ silent: true })
      void loadCatalog(selectedAccountId.value, { preserveModel: true, silent: true })
      if ((streamStatus.value as StreamStatus) === 'completed') {
        pendingStreamRequest.value = null
        appStore.showSuccess(t('admin.channelTest.testSucceeded'))
      }
    }
  }
}

async function sendTurn(): Promise<void> {
  if (running.value || sendPending.value) return
  if (!selectedModelId.value || selectedModel.value == null) {
    appStore.showError(t('admin.channelTest.selectModelToChat'))
    return
  }
  if (selectedModel.value.downstream_allowed === false) {
    appStore.showError(t('admin.channelTest.downstreamClosed'))
    return
  }
  const text = prompt.value.trim()
  if (!text) {
    appStore.showError(t('admin.channelTest.promptRequired'))
    return
  }

  sendPending.value = true
  let activeConversation: AvailabilityConversation
  try {
    activeConversation = await ensureConversation(text)
  } catch (error) {
    sendPending.value = false
    streamStatus.value = 'failed'
    streamError.value = errorText(error, t('admin.channelTest.conversationFailed'))
    prompt.value = text
    appStore.showError(streamError.value)
    return
  }

  const localTurn = makeLocalTurn(activeConversation.id, text)
  activeTurnPrompt.value = text
  activeTurnId.value = localTurn.id
  upsertTurn(localTurn)
  liveEvents.value = []
  streamError.value = ''
  streamStatus.value = 'routing'
  lastStreamSeq.value = null
  const request: PendingStreamRequest = {
    conversationId: activeConversation.id,
    prompt: text,
    clientRequestId: newClientRequestId()
  }
  pendingStreamRequest.value = request
  prompt.value = ''
  sendPending.value = false
  await runStreamRequest(request)
}

async function resumeTurn(): Promise<void> {
  const request = pendingStreamRequest.value
  if (!request || running.value || sendPending.value || conversation.value?.id !== request.conversationId) return
  streamError.value = ''
  streamStatus.value = 'routing'
  await runStreamRequest(request)
}

function cancelTurn(): void {
  if (!running.value) return
  streamController?.abort()
}

async function refreshActiveConversation(options: { silent?: boolean } = {}): Promise<AvailabilityConversationDetail | null> {
  const id = conversation.value?.id
  if (id == null) return null
  try {
    const detail = await adminAPI.availability.getConversation(id, { page: 1, page_size: 50 })
    if (!conversation.value || conversation.value.id !== id) return null
    conversation.value = detail.conversation
    turns.value = sortTurns(detail.turns)
    conversationTurnTotal.value = detail.total ?? detail.turns.length
    conversationTurnPage.value = detail.page ?? 1
    return detail
  } catch (error) {
    if (!options.silent) {
      streamError.value = errorText(error, t('admin.channelTest.conversationFailed'))
      appStore.showError(streamError.value)
    }
    return null
  }
}

async function refreshConversationStatus(): Promise<void> {
  const detail = await refreshActiveConversation()
  const latest = detail?.turns ? sortTurns(detail.turns).at(-1) : null
  if (!latest) return
  if (latest.status === 'succeeded') streamStatus.value = 'completed'
  else if (latest.status === 'failed') streamStatus.value = 'failed'
  else if (latest.status === 'cancelled') streamStatus.value = 'cancelled'
  else streamStatus.value = 'disconnected'
  if (latest.status !== 'running') streamError.value = latest.error || ''
}

async function loadMoreTurns(): Promise<void> {
  if (!conversation.value || loadingMoreTurns.value || !hasMoreTurns.value) return
  loadingMoreTurns.value = true
  try {
    const nextPage = conversationTurnPage.value + 1
    const detail = await adminAPI.availability.getConversation(conversation.value.id, { page: nextPage, page_size: 50 })
    const byId = new Map<string, AvailabilityTurn>()
    for (const turn of turns.value) byId.set(String(turn.id), turn)
    for (const turn of detail.turns) {
      const existing = byId.get(String(turn.id))
      byId.set(String(turn.id), existing ? mergeTurns(existing, turn) : turn)
    }
    turns.value = sortTurns([...byId.values()])
    conversationTurnTotal.value = detail.total ?? Math.max(conversationTurnTotal.value, turns.value.length)
    conversationTurnPage.value = detail.page ?? nextPage
  } catch (error) {
    appStore.showError(errorText(error, t('admin.channelTest.conversationFailed')))
  } finally {
    loadingMoreTurns.value = false
  }
}

async function loadHistory(options: { silent?: boolean } = {}): Promise<void> {
  historyController?.abort()
  const controller = new AbortController()
  historyController = controller
  const requestVersion = ++historyRequestVersion
  loadingHistory.value = true
  try {
    const result = await adminAPI.availability.listConversations(
      { page: historyPage.value, page_size: historyPageSize, search: historySearch.value },
      { signal: controller.signal }
    )
    if (requestVersion !== historyRequestVersion) return
    historyItems.value = result.items || []
    historyTotal.value = result.total || 0
    if (historyPage.value > historyPages.value) historyPage.value = historyPages.value
    historyError.value = ''
  } catch (error) {
    if (isAbortError(error) || controller.signal.aborted) return
    if (requestVersion === historyRequestVersion) {
      historyError.value = errorText(error, t('admin.channelTest.historyFailed'))
      if (!options.silent) appStore.showError(historyError.value)
    }
  } finally {
    if (requestVersion === historyRequestVersion) loadingHistory.value = false
  }
}

function changeHistoryPage(delta: number): void {
  const nextPage = historyPage.value + delta
  if (nextPage < 1 || nextPage > historyPages.value || nextPage === historyPage.value) return
  historyPage.value = nextPage
  void loadHistory({ silent: true })
}

function historyGroupName(groupId: number): string {
  return groups.value.find(group => group.id === groupId)?.name ?? `#${groupId}`
}

function historyAccountName(item: AvailabilityConversation): string {
  if (item.account_id == null) return t('admin.channelTest.automaticAccount')
  return accounts.value.find(account => account.id === item.account_id)?.name ?? `#${item.account_id}`
}

async function restoreConversation(item: AvailabilityConversation): Promise<void> {
  if (running.value) return
  const requestVersion = ++restoreRequestVersion
  loadingConversationId.value = item.id
  conversationLoading.value = true
  try {
    const detail = await adminAPI.availability.getConversation(item.id, { page: 1, page_size: 50 })
    if (requestVersion !== restoreRequestVersion) return
    const restored = detail.conversation
    isApplyingRestoredScope = true
    try {
      selectedGroupId.value = restored.group_id
      selectedAccountId.value = restored.account_id
      selectedModelId.value = restored.model
      resetConversation()
      conversation.value = restored
      turns.value = sortTurns(detail.turns)
      conversationTurnTotal.value = detail.total ?? detail.turns.length
      conversationTurnPage.value = detail.page ?? 1
      liveEvents.value = []
      streamError.value = ''
      streamStatus.value = 'idle'
      lastStreamSeq.value = null
      catalog.value = null
    } finally {
      isApplyingRestoredScope = false
    }
    await loadCatalog(restored.account_id, { preserveModel: true, silent: true })
  } catch (error) {
    if (requestVersion === restoreRequestVersion) {
      streamError.value = errorText(error, t('admin.channelTest.conversationFailed'))
      appStore.showError(streamError.value)
    }
  } finally {
    if (requestVersion === restoreRequestVersion) {
      conversationLoading.value = false
      loadingConversationId.value = null
    } else if (loadingConversationId.value === item.id) {
      conversationLoading.value = false
      loadingConversationId.value = null
    }
  }
}

watch(selectedGroupId, (next, previous) => {
  if (next !== previous) void handleGroupChange()
}, { flush: 'sync' })

watch(selectedModelId, (next, previous) => {
  if (!isBootstrapping && !isApplyingRestoredScope && next !== previous && conversation.value && !running.value) {
    restoreRequestVersion += 1
    resetConversation()
  }
})

watch(historySearch, () => {
  historyPage.value = 1
  if (historySearchTimer) clearTimeout(historySearchTimer)
  historySearchTimer = setTimeout(() => {
    void loadHistory({ silent: true })
  }, 250)
})

onMounted(async () => {
  await Promise.all([loadGroups(), loadHistory({ silent: true })])
  isBootstrapping = false
  if (selectedGroupId.value != null) await loadCatalog(null)
})

onUnmounted(() => {
  catalogController?.abort()
  historyController?.abort()
  streamController?.abort()
  if (historySearchTimer) clearTimeout(historySearchTimer)
  streamRunVersion += 1
  restoreRequestVersion += 1
})
</script>

<style scoped>
.availability-scrollbar {
  scrollbar-width: thin;
  scrollbar-color: rgba(148, 163, 184, 0.55) transparent;
}

details[open] .history-chevron {
  transform: rotate(180deg);
}
</style>
