BEGIN;

-- Складские действия (ADR-0001): операция режется на части-действия, черновик до submit.

CREATE TABLE public.warehouse_actions (
  action_id uuid PRIMARY KEY,
  operation_id uuid NOT NULL,
  part_index smallint NOT NULL,
  action_type smallint NOT NULL CHECK (action_type BETWEEN 1 AND 4),
  signer_credential_id bytea NOT NULL REFERENCES public.passkey_credentials(credential_id) ON DELETE RESTRICT,
  created_by uuid NOT NULL REFERENCES public.users(user_id) ON DELETE RESTRICT,
  item_ids uuid[] NOT NULL,
  salt bytea NOT NULL CHECK (octet_length(salt) = 32),
  details_hash bytea NOT NULL CHECK (octet_length(details_hash) = 32),
  challenge bytea NOT NULL CHECK (octet_length(challenge) = 32),
  details jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  submitted_at timestamptz,
  UNIQUE (operation_id, part_index)
);
CREATE INDEX idx_warehouse_actions_items ON public.warehouse_actions USING gin (item_ids);
ALTER TABLE wms_ops.receiving_table ADD COLUMN operation_id uuid;

COMMENT ON TABLE public.warehouse_actions IS 'Складские действия (ADR-0001 §10): одна строка — одна часть операции, черновик до submit, после submit — конверт в outbox_events.payload; детали в details, доменные строки ссылаются на operation_id.';

INSERT INTO public.schema_migrations (version) VALUES (16) ON CONFLICT (version) DO NOTHING;

COMMIT;
