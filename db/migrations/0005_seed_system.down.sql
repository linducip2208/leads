-- reverse of 0005
DELETE FROM plans WHERE slug IN ('free','starter','professional','business','enterprise');
DELETE FROM permissions;
