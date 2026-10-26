-- 将新用户及注册来源的默认并发统一为 10。
-- 仅替换仍保留初始默认值 5 的设置；管理员已经改过的值保持不变。

ALTER TABLE users
    ALTER COLUMN concurrency SET DEFAULT 10;

UPDATE settings
SET value = '10'
WHERE key IN (
    'auth_source_default_email_concurrency',
    'auth_source_default_linuxdo_concurrency',
    'auth_source_default_oidc_concurrency',
    'auth_source_default_wechat_concurrency',
    'auth_source_default_github_concurrency',
    'auth_source_default_google_concurrency',
    'auth_source_default_dingtalk_concurrency'
)
AND value = '5';
