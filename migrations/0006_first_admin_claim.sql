-- The first-admin claim is its own record, not a count of cluster admins:
-- deleting every admin must not reopen it. Deliberately without a foreign key
-- to users, so deleting the claimant leaves the claim taken.
CREATE TABLE first_admin_claim (
    singleton   BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    identity_id UUID        NOT NULL,
    claimed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
