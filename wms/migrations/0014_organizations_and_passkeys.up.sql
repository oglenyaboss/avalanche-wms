BEGIN;

-- Организации (владельцы сотрудников) и учётные данные passkey (WebAuthn, P-256).

CREATE TABLE public.organizations (
  org_id uuid PRIMARY KEY,
  name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT organizations_name_unique UNIQUE (name)
);

COMMENT ON TABLE public.organizations IS 'Организации, которым принадлежат пользователи WMS.';
COMMENT ON COLUMN public.organizations.org_id IS 'Primary key (UUID).';
COMMENT ON COLUMN public.organizations.name IS 'Уникальное название организации.';

ALTER TABLE public.users ADD COLUMN org_id uuid REFERENCES public.organizations(org_id) ON DELETE RESTRICT;
CREATE INDEX idx_users_org_id ON public.users (org_id);

COMMENT ON COLUMN public.users.org_id IS 'FK to organizations; NULL для пользователей без организации.';

CREATE TABLE public.passkey_credentials (
  credential_id bytea PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES public.users(user_id) ON DELETE CASCADE,
  org_id uuid NOT NULL REFERENCES public.organizations(org_id) ON DELETE RESTRICT,
  qx bytea NOT NULL CHECK (octet_length(qx) = 32),
  qy bytea NOT NULL CHECK (octet_length(qy) = 32),
  key_id bytea NOT NULL CHECK (octet_length(key_id) = 32),
  role text NOT NULL CHECK (role IN ('ADMIN', 'OPERATOR')),
  status text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'ACTIVE', 'REJECTED', 'REVOKED')),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT passkey_credentials_key_id_unique UNIQUE (key_id)
);
CREATE UNIQUE INDEX passkey_credentials_one_live_per_user ON public.passkey_credentials (user_id) WHERE status IN ('PENDING', 'ACTIVE');
CREATE INDEX idx_passkey_credentials_org_id ON public.passkey_credentials (org_id);
CREATE TRIGGER trg_passkey_credentials_set_updated_at
BEFORE UPDATE ON public.passkey_credentials
FOR EACH ROW
EXECUTE FUNCTION public.set_updated_at();

COMMENT ON TABLE public.passkey_credentials IS 'Passkey (WebAuthn) учётные данные пользователей; публичный ключ P-256 и статус регистрации в реестре.';
COMMENT ON COLUMN public.passkey_credentials.credential_id IS 'WebAuthn credential id (сырые байты).';
COMMENT ON COLUMN public.passkey_credentials.user_id IS 'FK to users.';
COMMENT ON COLUMN public.passkey_credentials.org_id IS 'FK to organizations (организация пользователя на момент регистрации).';
COMMENT ON COLUMN public.passkey_credentials.qx IS 'Координата X публичного ключа P-256 (32 байта).';
COMMENT ON COLUMN public.passkey_credentials.qy IS 'Координата Y публичного ключа P-256 (32 байта).';
COMMENT ON COLUMN public.passkey_credentials.key_id IS 'keccak256(qx || qy) — идентификатор ключа в блокчейн-реестре.';
COMMENT ON COLUMN public.passkey_credentials.role IS 'Роль пользователя на момент регистрации: ADMIN/OPERATOR.';
COMMENT ON COLUMN public.passkey_credentials.status IS 'Статус: PENDING/ACTIVE/REJECTED/REVOKED.';

INSERT INTO public.schema_migrations (version) VALUES (14) ON CONFLICT (version) DO NOTHING;

COMMIT;
