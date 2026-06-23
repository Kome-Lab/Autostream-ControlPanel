INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'integrations.read' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'integrations.create' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'integrations.update' FROM roles WHERE name = 'super_admin';

INSERT IGNORE INTO role_permissions (role_id, permission)
SELECT id, 'integrations.delete' FROM roles WHERE name = 'super_admin';
