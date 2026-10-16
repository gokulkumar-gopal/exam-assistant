package dynamo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

type userItem struct {
	ID                 string `dynamodbav:"id"`
	Name               string `dynamodbav:"name"`
	Email              string `dynamodbav:"email"`
	PasswordHash       string `dynamodbav:"passwordHash,omitempty"`
	GoogleSub          string `dynamodbav:"googleSub,omitempty"`
	Role               string `dynamodbav:"role"`
	SessionsValidAfter int64  `dynamodbav:"sessionsValidAfter,omitempty"`
	CreatedAt          string `dynamodbav:"createdAt"`
}

type lockItem struct {
	ID     string `dynamodbav:"id"`
	UserID string `dynamodbav:"userId"`
}

const notExists = "attribute_not_exists(id)"

func (s *Stores) putIfAbsent(table string, item any) (types.TransactWriteItem, error) {
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return types.TransactWriteItem{Put: &types.Put{
		TableName: &table, Item: av, ConditionExpression: ptr(notExists),
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	}}, nil
}

func (s *Stores) CreateUser(ctx context.Context, u store.User) error {
	if u.ID == "" || u.Email == "" {
		return errors.New("dynamo: user needs an id and an email")
	}
	if isLockID(u.ID) {
		return errors.New("dynamo: user id uses a reserved prefix")
	}
	locks := []lockItem{{ID: emailLockPrefix + u.Email, UserID: u.ID}}
	if u.GoogleSub != "" {
		locks = append(locks, lockItem{ID: googleLockPrefix + u.GoogleSub, UserID: u.ID})
	}
	items := make([]types.TransactWriteItem, 0, 1+len(locks))
	it, err := s.putIfAbsent(s.users, userItem{
		ID: u.ID, Name: u.Name, Email: u.Email, PasswordHash: u.PasswordHash, GoogleSub: u.GoogleSub,
		Role: u.Role, SessionsValidAfter: u.SessionsValidAfter, CreatedAt: u.CreatedAt.UTC().Format(timeLayout),
	})
	if err != nil {
		return err
	}
	items = append(items, it)
	for _, l := range locks {
		it, err := s.putIfAbsent(s.users, l)
		if err != nil {
			return err
		}
		items = append(items, it)
	}
	_, err = s.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if err != nil {
		if rs, ok := cancelReasons(err); ok {
			for i := range rs {
				if reasonIsCondFailed(rs, i) {
					return fmt.Errorf("%w: user, email or google subject already exists", store.ErrConflict)
				}
			}
		}
		return fmt.Errorf("dynamo: create user: %w", err)
	}
	return nil
}

func (s *Stores) CreateGoogleUser(ctx context.Context, u store.User) error {
	if u.GoogleSub == "" {
		return errors.New("dynamo: google user needs a GoogleSub")
	}
	return s.CreateUser(ctx, u)
}

func (s *Stores) LinkGoogle(ctx context.Context, userID, sub string, validAfter int64) error {
	if userID == "" || sub == "" {
		return errors.New("dynamo: link needs a user id and a google subject")
	}
	if isLockID(userID) {
		return store.ErrNotFound
	}
	lock, err := s.putIfAbsent(s.users, lockItem{ID: googleLockPrefix + sub, UserID: userID})
	if err != nil {
		return err
	}
	subAV, _ := attributevalue.Marshal(sub)
	vaAV, _ := attributevalue.Marshal(validAfter)
	upd := types.TransactWriteItem{Update: &types.Update{
		TableName:                           &s.users,
		Key:                                 map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: userID}},
		ConditionExpression:                 ptr("attribute_exists(id) AND attribute_not_exists(googleSub)"),
		UpdateExpression:                    ptr("SET googleSub = :sub, sessionsValidAfter = :va REMOVE passwordHash"),
		ExpressionAttributeValues:           map[string]types.AttributeValue{":sub": subAV, ":va": vaAV},
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	}}
	_, err = s.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{lock, upd}})
	if err == nil {
		return nil
	}
	if rs, ok := cancelReasons(err); ok {
		// Item 1 is the user update. With ALL_OLD, an empty Item means the user is missing;
		// otherwise it exists and already has a GoogleSub. A missing user wins over a taken lock.
		if reasonIsCondFailed(rs, 1) {
			if len(rs[1].Item) == 0 {
				return store.ErrNotFound
			}
			return fmt.Errorf("%w: user is already linked to a google account", store.ErrConflict)
		}
		if reasonIsCondFailed(rs, 0) {
			return fmt.Errorf("%w: google account belongs to another user", store.ErrConflict)
		}
	}
	return fmt.Errorf("dynamo: link google: %w", err)
}

func userKey(id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}}
}

func (u userItem) toUser() (store.User, error) {
	t, err := time.Parse(timeLayout, u.CreatedAt)
	if err != nil {
		return store.User{}, fmt.Errorf("dynamo: user %s createdAt: %w", u.ID, err)
	}
	return store.User{ID: u.ID, Name: u.Name, Email: u.Email, PasswordHash: u.PasswordHash, GoogleSub: u.GoogleSub,
		Role: u.Role, SessionsValidAfter: u.SessionsValidAfter, CreatedAt: t}, nil
}

func decodeUser(m map[string]types.AttributeValue) (store.User, error) {
	var it userItem
	if err := attributevalue.UnmarshalMap(m, &it); err != nil {
		return store.User{}, fmt.Errorf("dynamo: decode user: %w", err)
	}
	return it.toUser()
}

func (s *Stores) getItem(ctx context.Context, table string, key map[string]types.AttributeValue) (map[string]types.AttributeValue, error) {
	out, err := s.api.GetItem(ctx, &dynamodb.GetItemInput{TableName: &table, Key: key, ConsistentRead: ptr(true)})
	if err != nil {
		return nil, fmt.Errorf("dynamo: get %s: %w", table, err)
	}
	if len(out.Item) == 0 {
		return nil, store.ErrNotFound
	}
	return out.Item, nil
}

func (s *Stores) GetByID(ctx context.Context, id string) (store.User, error) {
	if id == "" || isLockID(id) {
		return store.User{}, store.ErrNotFound
	}
	m, err := s.getItem(ctx, s.users, userKey(id))
	if err != nil {
		return store.User{}, err
	}
	return decodeUser(m)
}

func (s *Stores) viaLock(ctx context.Context, lockID string) (store.User, error) {
	m, err := s.getItem(ctx, s.users, userKey(lockID))
	if err != nil {
		return store.User{}, err
	}
	var l lockItem
	if err := attributevalue.UnmarshalMap(m, &l); err != nil {
		return store.User{}, fmt.Errorf("dynamo: decode lock: %w", err)
	}
	return s.GetByID(ctx, l.UserID)
}

func (s *Stores) GetByEmail(ctx context.Context, email string) (store.User, error) {
	if email == "" {
		return store.User{}, store.ErrNotFound
	}
	return s.viaLock(ctx, emailLockPrefix+email)
}

func (s *Stores) GetByGoogleSub(ctx context.Context, sub string) (store.User, error) {
	if sub == "" {
		return store.User{}, store.ErrNotFound
	}
	return s.viaLock(ctx, googleLockPrefix+sub)
}

// uniqueKeys drops empty and duplicate values, keeping first-seen order.
func uniqueKeys(vals []string) []string {
	seen := make(map[string]bool, len(vals))
	var out []string
	for _, v := range vals {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *Stores) GetManyByID(ctx context.Context, ids []string) (map[string]store.User, error) {
	var keys []map[string]types.AttributeValue
	for _, id := range uniqueKeys(ids) {
		if !isLockID(id) {
			keys = append(keys, userKey(id))
		}
	}
	items, err := s.batchGet(ctx, s.users, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.User, len(items))
	for _, m := range items {
		u, err := decodeUser(m)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, nil
}

func (s *Stores) GetManyByEmail(ctx context.Context, emails []string) (map[string]store.User, error) {
	var keys []map[string]types.AttributeValue
	for _, e := range uniqueKeys(emails) {
		keys = append(keys, userKey(emailLockPrefix+e))
	}
	locks, err := s.batchGet(ctx, s.users, keys)
	if err != nil {
		return nil, err
	}
	idByEmail := make(map[string]string, len(locks))
	var ids []string
	for _, m := range locks {
		var l lockItem
		if err := attributevalue.UnmarshalMap(m, &l); err != nil {
			return nil, fmt.Errorf("dynamo: decode lock: %w", err)
		}
		idByEmail[l.ID[len(emailLockPrefix):]] = l.UserID
		ids = append(ids, l.UserID)
	}
	byID, err := s.GetManyByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.User, len(idByEmail))
	for email, id := range idByEmail {
		if u, ok := byID[id]; ok {
			out[email] = u
		}
	}
	return out, nil
}
