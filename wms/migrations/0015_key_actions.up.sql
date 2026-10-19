BEGIN;

-- Подписанные ключевые действия (ADR-0001): черновик в key_actions, конверт в outbox_events.payload.

ALTER TABLE public.outbox_events ADD COLUMN payload jsonb;
COMMENT ON COLUMN public.outbox_events.payload IS 'Конверт подписанного действия (ADR-0001 §9); NULL у событий на товар.';

ALTER TABLE public.onchain_events ADD COLUMN reject_reason smallint;
COMMENT ON COLUMN public.onchain_events.reject_reason IS 'Код RejectReason (ADR-0001 §6), если контракт отклонил действие.';

CREATE TABLE public.key_actions (
  action_id uuid PRIMARY KEY,
  org_id uuid NOT NULL REFERENCES public.organizations(org_id) ON DELETE RESTRICT,
  action_type smallint NOT NULL CHECK (action_type IN (5, 6)),
  target_credential_id bytea NOT NULL REFERENCES public.passkey_credentials(credential_id) ON DELETE RESTRICT,
  signer_credential_id bytea NOT NULL REFERENCES public.passkey_credentials(credential_id) ON DELETE RESTRICT,
  challenge bytea NOT NULL CHECK (octet_length(challenge) = 32),
  created_by uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now(),
  submitted_at timestamptz
);
CREATE INDEX idx_key_actions_target ON public.key_actions (target_credential_id, submitted_at DESC);

COMMENT ON TABLE public.key_actions IS 'Подписанные ключевые действия (ADR-0001): черновик до submit, после submit — строка outbox.';
COMMENT ON COLUMN public.key_actions.action_id IS 'UUIDv7 действия = event_id/aggregate_id события outbox.';
COMMENT ON COLUMN public.key_actions.org_id IS 'FK to organizations.';
COMMENT ON COLUMN public.key_actions.action_type IS 'Тип действия: 5 KeyRegister, 6 KeyRevoke.';
COMMENT ON COLUMN public.key_actions.target_credential_id IS 'FK to passkey_credentials: ключ, который регистрируют или отзывают.';
COMMENT ON COLUMN public.key_actions.signer_credential_id IS 'FK to passkey_credentials: ключ админа, который подписывает.';
COMMENT ON COLUMN public.key_actions.challenge IS 'Challenge (32 байта), который подписывает админ.';
COMMENT ON COLUMN public.key_actions.created_by IS 'FK to users: кто создал черновик.';
COMMENT ON COLUMN public.key_actions.submitted_at IS 'NULL = черновик; после submit — момент отправки.';

INSERT INTO public.schema_migrations (version) VALUES (15) ON CONFLICT (version) DO NOTHING;

COMMIT;
