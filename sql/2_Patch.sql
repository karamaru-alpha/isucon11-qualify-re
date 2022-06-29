ALTER TABLE `isu_condition` ADD COLUMN `level` VARCHAR(255) NOT NULL;

UPDATE `isu_condition` SET `level` = 'critical' WHERE CHAR_LENGTH(`condition`) = 50;
UPDATE `isu_condition` SET `level` = 'warning' WHERE CHAR_LENGTH(`condition`) = 49 OR CHAR_LENGTH(`condition`) = 48;
UPDATE `isu_condition` SET `level` = 'info' WHERE CHAR_LENGTH(`condition`) = 47;
