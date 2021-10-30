package services

import (
	"bbs-go/cache"
	"bbs-go/model"
	"bbs-go/model/constants"
	"bbs-go/pkg/mq"
	"bbs-go/repositories"
	"github.com/emirpasic/gods/sets/hashset"
	"github.com/mlogclub/simple"
	"github.com/mlogclub/simple/date"
	"gorm.io/gorm"
)

var UserFollowService = newUserFollowService()

func newUserFollowService() *userFollowService {
	return &userFollowService{}
}

type userFollowService struct {
}

func (s *userFollowService) Get(id int64) *model.UserFollow {
	return repositories.UserFollowRepository.Get(simple.DB(), id)
}

func (s *userFollowService) Take(where ...interface{}) *model.UserFollow {
	return repositories.UserFollowRepository.Take(simple.DB(), where...)
}

func (s *userFollowService) Find(cnd *simple.SqlCnd) []model.UserFollow {
	return repositories.UserFollowRepository.Find(simple.DB(), cnd)
}

func (s *userFollowService) FindOne(cnd *simple.SqlCnd) *model.UserFollow {
	return repositories.UserFollowRepository.FindOne(simple.DB(), cnd)
}

func (s *userFollowService) FindPageByParams(params *simple.QueryParams) (list []model.UserFollow, paging *simple.Paging) {
	return repositories.UserFollowRepository.FindPageByParams(simple.DB(), params)
}

func (s *userFollowService) FindPageByCnd(cnd *simple.SqlCnd) (list []model.UserFollow, paging *simple.Paging) {
	return repositories.UserFollowRepository.FindPageByCnd(simple.DB(), cnd)
}

func (s *userFollowService) Count(cnd *simple.SqlCnd) int64 {
	return repositories.UserFollowRepository.Count(simple.DB(), cnd)
}

func (s *userFollowService) Create(t *model.UserFollow) error {
	return repositories.UserFollowRepository.Create(simple.DB(), t)
}

func (s *userFollowService) Update(t *model.UserFollow) error {
	return repositories.UserFollowRepository.Update(simple.DB(), t)
}

func (s *userFollowService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.UserFollowRepository.Updates(simple.DB(), id, columns)
}

func (s *userFollowService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.UserFollowRepository.UpdateColumn(simple.DB(), id, name, value)
}

func (s *userFollowService) Delete(id int64) {
	repositories.UserFollowRepository.Delete(simple.DB(), id)
}

func (s *userFollowService) Follow(userId, otherId int64) error {
	if userId == otherId {
		// 自己关注自己，不进行处理。
		// return simple.NewErrorMsg("自己不能关注自己")
		return nil
	}

	if s.isFollow(userId, otherId) {
		return nil
	}

	err := simple.DB().Transaction(func(tx *gorm.DB) error {
		// 如果对方也关注了我，那么更新状态为互相关注
		otherFollowed := tx.Exec("update t_user_follow set status = ? where user_id = ? and other_id = ?",
			constants.FollowStatusBoth, otherId, userId).RowsAffected > 0
		status := constants.FollowStatusFollow
		if otherFollowed {
			status = constants.FollowStatusBoth
		}

		if err := repositories.UserRepository.Updates(tx, userId, map[string]interface{}{
			"follow_count": gorm.Expr("follow_count + 1"),
		}); err != nil {
			return err
		}
		cache.UserCache.Invalidate(userId)

		if err := repositories.UserRepository.Updates(tx, otherId, map[string]interface{}{
			"fans_count": gorm.Expr("fans_count + 1"),
		}); err != nil {
			return err
		}
		cache.UserCache.Invalidate(otherId)

		return repositories.UserFollowRepository.Create(tx, &model.UserFollow{
			UserId:     userId,
			OtherId:    otherId,
			Status:     status,
			CreateTime: date.NowTimestamp(),
		})
	})
	if err != nil {
		return err
	}

	// 发送mq消息
	mq.Send(mq.EventTypeFollow, mq.FollowEvent{
		UserId:  userId,
		OtherId: otherId,
	})
	return nil
}

func (s *userFollowService) UnFollow(userId, otherId int64) error {
	if userId == otherId {
		// 自己关注自己，不进行处理。
		return nil
	}
	if !s.isFollow(userId, otherId) {
		return nil
	}
	err := simple.DB().Transaction(func(tx *gorm.DB) error {
		success := tx.Where("user_id = ? and other_id = ?", userId, otherId).Delete(model.UserFollow{}).RowsAffected > 0
		if success {
			tx.Exec("update t_user_follow set status = ? where user_id = ? and other_id = ?",
				constants.FollowStatusFollow, otherId, userId)
		}

		if err := tx.Model(&model.User{}).Where("id = ? and follow_count > 0", userId).Updates(map[string]interface{}{
			"follow_count": gorm.Expr("follow_count - 1"),
		}).Error; err != nil {
			return err
		}
		cache.UserCache.Invalidate(userId)

		if err := tx.Model(&model.User{}).Where("id = ? and fans_count > 0", otherId).Updates(map[string]interface{}{
			"fans_count": gorm.Expr("fans_count - 1"),
		}).Error; err != nil {
			return err
		}
		cache.UserCache.Invalidate(otherId)

		return nil
	})
	if err != nil {
		return err
	}

	// 发送mq消息
	mq.Send(mq.EventTypeUnFollow, mq.UnFollowEvent{
		UserId:  userId,
		OtherId: otherId,
	})
	return nil
}

// isFollow 是否关注
func (s *userFollowService) isFollow(userId, otherId int64) bool {
	t := s.FindOne(simple.NewSqlCnd().Eq("user_id", userId).Eq("other_id", otherId))
	return t != nil
}

// GetFans 粉丝列表
func (s *userFollowService) GetFans(userId int64, cursor int64) (itemList []int64, nextCursor int64, hasMore bool) {
	limit := 20
	cnd := simple.NewSqlCnd().Eq("other_id", userId)
	if cursor > 0 {
		cnd.Lt("id", cursor)
	}
	cnd.Desc("id").Limit(limit)
	list := repositories.UserFollowRepository.Find(simple.DB(), cnd)

	if len(list) > 0 {
		nextCursor = list[len(list)-1].Id
		hasMore = len(list) >= limit
		for _, e := range list {
			itemList = append(itemList, e.UserId)
		}
	} else {
		nextCursor = cursor
	}
	return
}

// GetFollows 关注列表
func (s *userFollowService) GetFollows(userId int64, cursor int64) (itemList []int64, nextCursor int64, hasMore bool) {
	limit := 20
	cnd := simple.NewSqlCnd().Eq("user_id", userId)
	if cursor > 0 {
		cnd.Lt("id", cursor)
	}
	cnd.Desc("id").Limit(limit)
	list := repositories.UserFollowRepository.Find(simple.DB(), cnd)

	if len(list) > 0 {
		nextCursor = list[len(list)-1].Id
		hasMore = len(list) >= limit
		for _, e := range list {
			itemList = append(itemList, e.OtherId)
		}
	} else {
		nextCursor = cursor
	}
	return
}

// ScanFans 扫描粉丝
func (s *userFollowService) ScanFans(userId int64, handle func(fansId int64)) {
	var cursor int64 = 0
	for {
		list := s.Find(simple.NewSqlCnd().Eq("other_id", userId).Gt("id", cursor).Asc("id").Limit(100))
		if len(list) == 0 {
			break
		}
		cursor = list[len(list)-1].Id
		for _, item := range list {
			handle(item.UserId)
		}
	}
}

// ScanFollowed 扫描关注的用户
func (s *userFollowService) ScanFollowed(userId int64, handle func(followUserId int64)) {
	var cursor int64 = 0
	for {
		list := s.Find(simple.NewSqlCnd().Eq("user_id", userId).Gt("id", cursor).Asc("id").Limit(100))
		if len(list) == 0 {
			break
		}
		cursor = list[len(list)-1].Id
		for _, item := range list {
			handle(item.OtherId)
		}
	}
}

func (s *userFollowService) IsFollowed(userId, otherId int64) bool {
	if userId == otherId {
		return false
	}
	set := s.IsFollowedUsers(userId, otherId)
	return set.Contains(otherId)
}

func (s *userFollowService) IsFollowedUsers(userId int64, otherIds ...int64) hashset.Set {
	set := hashset.New()
	list := s.Find(simple.NewSqlCnd().Eq("user_id", userId).In("other_id", otherIds))
	for _, follow := range list {
		set.Add(follow.OtherId)
	}
	return *set
}
