-- Cluster admins are declared by the install rather than claimed by arriving,
-- so nothing reads this record any more. Dropping it is what makes the change
-- irreversible in the right direction: a downgrade that reintroduced the claim
-- would hand the role to whoever signed in next.
DROP TABLE IF EXISTS first_admin_claim;
