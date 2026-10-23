package model

// SchemaEpoch 是本版本理解的最新数据库结构纪元，迁移成功后记录在 schema_meta 表中。
//
// 每次修改 GORM 模型、AutoMigrate 列表或启动迁移逻辑时，都必须按以下规则判断：
//
//   - 兼容变更：新增可空列或带默认值的列、新增表、新增索引、只读取不改写旧数据的回填。
//     SchemaEpoch + 1，MinReaderEpoch 不变。旧版本仍可在迁移后的库上运行，
//     软更新失败或手动回滚时无需恢复快照。
//   - 破坏性变更：删除或重命名列、修改列类型或约束（含 GORM 会重建表的变更）、
//     新增无默认值的 NOT NULL 列、新增唯一约束，或旧代码继续写入会破坏数据的语义变化。
//     SchemaEpoch + 1，并令 MinReaderEpoch = SchemaEpoch。旧版本将拒绝启动（退出码 78），
//     回滚必须恢复更新前快照。
//
// schema_fingerprint_test.go 会在数据库结构变化时失败，提醒开发者按上述规则做出判断。
const SchemaEpoch = 1

// MinReaderEpoch 是被本版本迁移后的数据库要求读取方至少具备的纪元，必须不大于 SchemaEpoch。
// 数据库记录的值只增不减：一旦被破坏性版本迁移，更早的版本就无法再直接读取。
const MinReaderEpoch = 1
